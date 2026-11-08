package systems

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/CodeAIX/vpsmonitor/internal/common"
	"github.com/CodeAIX/vpsmonitor/internal/entities/container"
	"github.com/CodeAIX/vpsmonitor/internal/entities/system"
	"github.com/CodeAIX/vpsmonitor/internal/records"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

var HistoryPeriods = map[string]int64{"1m": 1, "10m": 10, "20m": 20, "120m": 120, "480m": 480}
var historyRetention = map[string]time.Duration{"1m": system.HistoryRetention, "10m": 12 * time.Hour, "20m": 24 * time.Hour, "120m": 7 * 24 * time.Hour, "480m": system.HistoryRetention}

func historyState(app core.App, id string) (*core.Record, error) {
	r, err := app.FindFirstRecordByFilter("agent_history_state", "system={:id}", dbx.Params{"id": id})
	if errors.Is(err, sql.ErrNoRows) {
		c, err := app.FindCollectionByNameOrId("agent_history_state")
		if err != nil {
			return nil, err
		}
		r = core.NewRecord(c)
		r.Set("system", id)
		return r, nil
	}
	return r, err
}

func validHistoryInfo(info *system.HistoryInfo) bool {
	if info == nil || info.Version != system.HistoryVersion {
		return false
	}
	switch info.RetentionSeconds {
	case int64(system.HistoryLegacyRetention / time.Second), int64(system.HistoryRetention / time.Second):
	default:
		return false
	}
	return info.Oldest >= 0 && info.Newest >= info.Oldest && info.Newest <= time.Now().Add(5*time.Minute).UnixMilli() &&
		info.Samples >= 0 && info.Samples <= int(info.RetentionSeconds/60)+1 &&
		info.Bytes >= 0 && info.Bytes <= 1024*1024*1024 && len(info.Error) <= 64
}

// Runs independently of current records and alerts, with at most two systems
// transferring in the background. Cancellation follows the system lifetime.
func (sys *System) startHistorySync(info *system.HistoryInfo) {
	if (info != nil && !validHistoryInfo(info)) || !sys.historySyncing.CompareAndSwap(false, true) {
		return
	}
	sys.manager.updaters.Go(func() {
		defer sys.historySyncing.Store(false)
		// Keep one pending worker per system so a long replay cannot make the
		// same systems miss every turn when several Agents reconnect together.
		select {
		case sys.manager.historySlots <- struct{}{}:
		case <-sys.ctx.Done():
			return
		}
		defer func() { <-sys.manager.historySlots }()
		ctx, cancel := context.WithTimeout(sys.ctx, 20*time.Second)
		defer cancel()
		if info == nil {
			_, _ = sys.manager.hub.DB().NewQuery("UPDATE agent_history_state SET supported=0 WHERE system={:id}").Bind(dbx.Params{"id": sys.Id}).WithContext(ctx).Execute()
			return
		}
		if err := sys.syncHistory(ctx, *info); err != nil && ctx.Err() == nil {
			sys.manager.hub.Logger().Warn("History sync will retry", "system", sys.Id, "err", err)
			state, findErr := historyState(sys.manager.hub, sys.Id)
			if findErr == nil {
				state.Set("supported", true)
				state.Set("retention_seconds", info.RetentionSeconds)
				state.Set("oldest", info.Oldest)
				state.Set("newest", info.Newest)
				state.Set("cache_samples", info.Samples)
				state.Set("cache_bytes", info.Bytes)
				state.Set("error", "sync_error")
				_ = sys.manager.hub.SaveWithContext(ctx, state)
			}
		}
	})
}

func (sys *System) syncHistory(ctx context.Context, info system.HistoryInfo) error {
	state, err := historyState(sys.manager.hub, sys.Id)
	if err != nil {
		return err
	}
	cursor := state.GetInt64("cursor")
	// Save diagnostics even while the initial journal is empty.
	if info.Newest <= cursor {
		return SaveHistoryPage(ctx, sys.manager.hub, sys.Id, info, system.HistoryRequest{After: cursor, Before: cursor, Limit: system.HistoryMaxPageSamples}, system.HistoryPage{Version: system.HistoryVersion, Next: cursor, Info: info}, time.Now())
	}
	for n := 0; n < 80 && ctx.Err() == nil; n++ {
		req := system.HistoryRequest{After: cursor, Before: info.Newest, Daily: info.DailyPages}
		req.Limit = req.MaxSamples()
		var page system.HistoryPage
		if err := sys.request(ctx, common.GetHistory, req, &page); err != nil {
			return err
		}
		if err := SaveHistoryPage(ctx, sys.manager.hub, sys.Id, info, req, page, time.Now()); err != nil {
			return err
		}
		cursor = page.Next
		if !page.More {
			return nil
		}
	}
	return ctx.Err()
}

// SaveHistoryPage advances the per-Hub cursor in the same transaction as raw
// samples and all chart tiers. No calls to createRecords or alert handlers occur.
func SaveHistoryPage(ctx context.Context, app core.App, id string, info system.HistoryInfo, req system.HistoryRequest, page system.HistoryPage, now time.Time) error {
	if !validHistoryInfo(&info) || page.Version != system.HistoryVersion ||
		req.Limit < 1 || req.Limit > req.MaxSamples() ||
		page.Next < req.After || page.Next > req.Before || (page.More && page.Next <= req.After) {
		return errors.New("invalid history page bounds")
	}
	if err := page.UnpackDaily(req); err != nil {
		return err
	}
	if len(page.Samples) > req.Limit {
		return errors.New("history page has too many samples")
	}
	budget := 2 * system.HistoryMaxPageBytes
	if req.Daily {
		budget = system.HistoryMaxDailyRawBytes + 4096 // Allow for the page envelope.
	}
	encoded, err := json.Marshal(page)
	if err != nil || len(encoded) > budget {
		return errors.New("history page too large")
	}
	last := req.After
	for _, sample := range page.Samples {
		if sample.At <= last || sample.At > page.Next || sample.Validate(now) != nil ||
			(req.Daily && sample.At/86400000 != page.Next/86400000) {
			return errors.New("invalid history sample order or value")
		}
		last = sample.At
	}
	return app.RunInTransaction(func(tx core.App) error {
		if _, err := tx.FindRecordById("systems", id); err != nil {
			return err
		}
		state, err := historyState(tx, id)
		if err != nil {
			return err
		}
		if state.GetInt64("cursor") < req.After {
			return errors.New("history cursor advanced without saved samples")
		}
		collection, err := tx.FindCollectionByNameOrId("agent_history_records")
		if err != nil {
			return err
		}
		touched := map[string]map[int64]bool{}
		for _, sample := range page.Samples {
			stamp := sample.At / 60000 * 60000
			existing, err := tx.FindFirstRecordByFilter("agent_history_records", "system={:id} && type='1m' && stamp={:stamp}", dbx.Params{"id": id, "stamp": stamp})
			if err == nil && existing != nil {
				continue
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			r := core.NewRecord(collection)
			r.Set("system", id)
			r.Set("type", "1m")
			r.Set("stamp", stamp)
			r.Set("first", sample.At)
			r.Set("last", sample.At)
			r.Set("samples", 1)
			r.Set("stats", sample.Stats)
			r.Set("containers", sample.Containers)
			b, _ := json.Marshal(sample)
			r.Set("bytes", len(b))
			if err := tx.SaveWithContext(ctx, r); err != nil {
				return err
			}
			for kind, minutes := range HistoryPeriods {
				if kind == "1m" {
					continue
				}
				width := minutes * 60000
				bucket := stamp / width * width
				if bucket+width < now.Add(-historyRetention[kind]).UnixMilli() {
					continue
				}
				if touched[kind] == nil {
					touched[kind] = map[int64]bool{}
				}
				touched[kind][bucket] = true
			}
		}
		for kind, buckets := range touched {
			for bucket := range buckets {
				if err := buildHistoryRollup(ctx, tx, collection, id, kind, bucket); err != nil {
					return err
				}
			}
		}
		cursor := state.GetInt64("cursor")
		if cursor > 0 && info.Oldest/60000 > cursor/60000+1 {
			state.Set("gap_from", (cursor/60000+1)*60000)
			state.Set("gap_to", info.Oldest)
		}
		previous := req.After
		for _, sample := range page.Samples {
			if previous > 0 && sample.At/60000 > previous/60000+1 {
				state.Set("gap_from", (previous/60000+1)*60000)
				state.Set("gap_to", sample.At)
			}
			previous = sample.At
		}
		state.Set("cursor", max(cursor, page.Next))
		state.Set("oldest", info.Oldest)
		state.Set("newest", info.Newest)
		state.Set("cache_samples", info.Samples)
		state.Set("cache_bytes", info.Bytes)
		state.Set("retention_seconds", info.RetentionSeconds)
		state.Set("supported", true)
		state.Set("limited", info.Limited)
		errorCode := info.Error
		if page.Info.Error != "" {
			errorCode = page.Info.Error
		}
		if len(errorCode) > 64 {
			return errors.New("invalid history diagnostic")
		}
		state.Set("error", errorCode)
		state.Set("last_sync", now.UnixMilli())
		if err := tx.SaveWithContext(ctx, state); err != nil {
			return err
		}
		if len(page.Samples) != 0 {
			return pruneHistory(ctx, tx, id, now)
		}
		return nil
	})
}

func buildHistoryRollup(ctx context.Context, app core.App, collection *core.Collection, id, kind string, bucket int64) error {
	var rows []*core.Record
	err := app.RecordQuery("agent_history_records").WithContext(ctx).AndWhere(dbx.NewExp("system={:id} AND type='1m' AND stamp >= {:from} AND stamp < {:to}", dbx.Params{"id": id, "from": bucket, "to": bucket + HistoryPeriods[kind]*60000})).OrderBy("stamp").Limit(481).All(&rows)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	stats := make([]system.Stats, 0, len(rows))
	containers := make([][]container.Stats, 0, len(rows))
	for _, r := range rows {
		var s system.Stats
		var c []container.Stats
		if err := r.UnmarshalJSONField("stats", &s); err != nil {
			return err
		}
		if err := r.UnmarshalJSONField("containers", &c); err != nil {
			return err
		}
		stats = append(stats, s)
		containers = append(containers, c)
	}
	r, err := app.FindFirstRecordByFilter("agent_history_records", "system={:id} && type={:type} && stamp={:stamp}", dbx.Params{"id": id, "type": kind, "stamp": bucket})
	if errors.Is(err, sql.ErrNoRows) {
		r = core.NewRecord(collection)
	} else if err != nil {
		return err
	}
	r.Set("system", id)
	r.Set("type", kind)
	r.Set("stamp", bucket)
	r.Set("samples", len(rows))
	r.Set("first", rows[0].GetInt64("first"))
	r.Set("last", rows[len(rows)-1].GetInt64("last"))
	r.Set("stats", records.AverageSystemStatsSlice(stats))
	r.Set("containers", records.AverageContainerStatsSlice(containers))
	return app.SaveWithContext(ctx, r)
}

func pruneHistory(ctx context.Context, app core.App, id string, now time.Time) error {
	for kind, retention := range historyRetention {
		if _, err := app.DB().NewQuery("DELETE FROM agent_history_records WHERE system={:id} AND type={:type} AND stamp < {:cutoff}").Bind(dbx.Params{"id": id, "type": kind, "cutoff": now.Add(-retention).UnixMilli() - HistoryPeriods[kind]*60000}).WithContext(ctx).Execute(); err != nil {
			return err
		}
	}
	// Bound additional Hub raw storage as well as the Agent spool. Rollups remain
	// available at the normal long-chart retention tiers after raw eviction.
	var total struct {
		Bytes int64 `db:"bytes"`
	}
	if err := app.DB().NewQuery("SELECT COALESCE(SUM(bytes),0) AS bytes FROM agent_history_records WHERE system={:id} AND type='1m'").Bind(dbx.Params{"id": id}).WithContext(ctx).One(&total); err != nil {
		return err
	}
	if total.Bytes > 256*1024*1024 {
		// Keep the newest suffix within budget without loading every raw row
		// into Go, including during a month of catch-up.
		_, err := app.DB().NewQuery(`
			DELETE FROM agent_history_records WHERE id IN (
				SELECT id FROM (
					SELECT id, SUM(bytes) OVER (ORDER BY stamp DESC ROWS UNBOUNDED PRECEDING) AS kept_bytes
					FROM agent_history_records WHERE system={:id} AND type='1m'
				) WHERE kept_bytes > {:budget}
			)`).Bind(dbx.Params{"id": id, "budget": 256 * 1024 * 1024}).WithContext(ctx).Execute()
		return err
	}
	return nil
}

// Cleanup also covers permanently disconnected systems, whose sync workers no
// longer run. Cascading relations handle systems that are actually deleted.
func CleanupHistory(app core.App, now time.Time) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var ids []struct {
		System string `db:"system"`
	}
	if err := app.DB().NewQuery("SELECT DISTINCT system FROM agent_history_records").WithContext(ctx).All(&ids); err != nil {
		return err
	}
	for _, id := range ids {
		if err := app.RunInTransaction(func(tx core.App) error { return pruneHistory(ctx, tx, id.System, now) }); err != nil {
			return err
		}
	}
	return nil
}
