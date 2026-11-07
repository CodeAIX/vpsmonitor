package systems

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/CodeAIX/vpsmonitor/internal/entities/traffic"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

// trafficWriter keeps at most one pending snapshot per system, at most fifty
// systems, and one writer. It never blocks the original monitoring updater.
type trafficWriter struct {
	mu      sync.Mutex
	pending map[string]*traffic.Snapshot
	running bool
	stopped bool
	done    chan struct{}
	manager *SystemManager
}

func (w *trafficWriter) enqueue(id string, snapshot *traffic.Snapshot) {
	if err := snapshot.Validate(time.Now()); err != nil {
		w.manager.hub.Logger().Warn("Rejected traffic snapshot", "system", id, "err", err)
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stopped || w.manager.ctx.Err() != nil {
		return
	}
	if previous, ok := w.pending[id]; ok && previous.ObservedAt >= snapshot.ObservedAt {
		return
	}
	if len(w.pending) >= 50 && w.pending[id] == nil {
		w.manager.hub.Logger().Warn("Traffic queue full; next absolute snapshot will retry", "system", id)
		return
	}
	w.pending[id] = snapshot
	if !w.running {
		w.running = true
		w.done = make(chan struct{})
		done := w.done
		w.manager.updaters.Go(func() { defer close(done); w.run() })
	}
}

func (w *trafficWriter) stop() {
	w.mu.Lock()
	w.stopped = true
	done := w.done
	w.mu.Unlock()
	if done != nil {
		<-done
	}
}

func (w *trafficWriter) run() {
	for {
		w.mu.Lock()
		if len(w.pending) == 0 || w.manager.ctx.Err() != nil {
			w.running = false
			w.mu.Unlock()
			return
		}
		var id string
		var snapshot *traffic.Snapshot
		for id, snapshot = range w.pending {
			break
		}
		delete(w.pending, id)
		w.mu.Unlock()
		ctx, cancel := context.WithTimeout(w.manager.ctx, 3*time.Second)
		err := SaveTrafficSnapshot(ctx, w.manager.hub, id, snapshot)
		cancel()
		if err != nil {
			w.manager.hub.Logger().Warn("Traffic persistence failed; original monitoring preserved", "system", id, "err", err)
		}
	}
}

func trafficRecord(ctx context.Context, app core.App, name string, where dbx.Expression) (*core.Record, error) {
	c, err := app.FindCachedCollectionByNameOrId(name)
	if err != nil {
		return nil, err
	}
	r := core.NewRecord(c)
	err = app.RecordQuery(c).WithContext(ctx).AndWhere(where).One(r)
	if errors.Is(err, sql.ErrNoRows) {
		return core.NewRecord(c), nil
	}
	return r, err
}

// SaveTrafficSnapshot is an internal, authenticated Agent ingestion path. The
// public API never accepts counters. Each transaction is independent of metrics.
func SaveTrafficSnapshot(ctx context.Context, app core.App, id string, snapshot *traffic.Snapshot) error {
	if err := snapshot.Validate(time.Now()); err != nil {
		return err
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(raw)
	hash := hex.EncodeToString(digest[:])
	return app.RunInTransaction(func(tx core.App) error {
		// Explicitly check that the system still exists; relation validation must
		// not be bypassed by a queued write after a system was deleted.
		var existing core.Record
		if err := tx.RecordQuery("systems").WithContext(ctx).AndWhere(dbx.HashExp{"id": id}).One(&existing); err != nil {
			return err
		}
		settings, err := trafficRecord(ctx, tx, "traffic_settings", dbx.HashExp{"system": id})
		if err != nil {
			return err
		}
		if settings.IsNew() {
			settings.Set("system", id)
			settings.Set("enabled", true)
			settings.Set("count_mode", "tx")
			settings.Set("billing_day", 1)
			settings.Set("billing_timezone", "UTC")
			settings.Set("display_unit", "GB")
		}
		last := settings.GetInt64("collector_last_observed_at")
		if snapshot.ObservedAt < last {
			return nil
		}
		if snapshot.ObservedAt == last {
			if settings.GetString("payload_digest") == hash {
				return nil
			}
			settings.Set("collector_status", "error")
			settings.Set("collector_error_code", "snapshot_conflict")
			return tx.SaveWithContext(ctx, settings)
		}
		settings.Set("collector_last_observed_at", snapshot.ObservedAt)
		settings.Set("collector_status", snapshot.Status)
		settings.Set("collector_error_code", snapshot.ErrorCode)
		settings.Set("payload_digest", hash)
		if snapshot.Status == traffic.OK {
			for _, iface := range snapshot.Interfaces {
				if err := saveTrafficInterface(ctx, tx, id, snapshot, iface); err != nil {
					return err
				}
			}
			var success struct {
				Last int64 `db:"last"`
			}
			if err := tx.DB().NewQuery("SELECT COALESCE(MAX(last_success_at),0) AS last FROM traffic_sources WHERE system={:system}").Bind(dbx.Params{"system": id}).WithContext(ctx).One(&success); err != nil {
				return err
			}
			settings.Set("collector_last_success_at", success.Last)
		}
		return tx.SaveWithContext(ctx, settings)
	})
}

func saveTrafficInterface(ctx context.Context, app core.App, id string, snapshot *traffic.Snapshot, iface traffic.InterfaceDaily) error {
	source, err := trafficRecord(ctx, app, "traffic_sources", dbx.HashExp{"system": id, "iface": iface.Name})
	if err != nil {
		return err
	}
	if source.IsNew() {
		source.Set("system", id)
		source.Set("iface", iface.Name)
		source.Set("first_seen_at", snapshot.ObservedAt)
		source.Set("source_created_at", iface.SourceCreatedAt)
		source.Set("quality", "normal")
	}
	source.Set("collector", "vnstat")
	source.Set("schema_version", snapshot.SchemaVersion)
	source.Set("timebase", "utc")
	source.Set("last_observed_at", snapshot.ObservedAt)
	previousUpdated := source.GetInt64("source_updated_at")
	source.Set("refresh_seconds", snapshot.RefreshSeconds)
	source.Set("save_interval_seconds", snapshot.SaveIntervalSeconds)
	source.Set("status", traffic.OK)
	if source.GetString("quality") != "suspected_reset" {
		source.Set("error_code", "")
	}
	if source.GetInt64("source_created_at") != iface.SourceCreatedAt {
		// Freeze the generation until an explicit future recovery operation.
		// Keeping the original created timestamp makes this sticky on retries.
		source.Set("quality", "suspected_reset")
		source.Set("error_code", "source_recreated")
		return app.SaveWithContext(ctx, source)
	}
	if iface.SourceUpdatedAt < previousUpdated {
		source.Set("quality", "suspected_reset")
		source.Set("error_code", "source_time_reversed")
		return app.SaveWithContext(ctx, source)
	}
	source.Set("utc_valid_from_day", snapshot.UTCValidFromDay)
	source.Set("source_updated_at", iface.SourceUpdatedAt)
	var records []*core.Record
	if len(iface.Days) > 0 {
		days := make([]any, len(iface.Days))
		for index, bucket := range iface.Days {
			days[index] = bucket.Day
		}
		if err := app.RecordQuery("traffic_daily").WithContext(ctx).AndWhere(dbx.HashExp{"system": id, "iface": iface.Name}).AndWhere(dbx.In("day", days...)).All(&records); err != nil {
			return err
		}
	}
	byDay := make(map[string]*core.Record, len(records))
	for _, record := range records {
		byDay[record.GetString("day")] = record
	}
	for _, bucket := range iface.Days {
		record := byDay[bucket.Day]
		if record == nil {
			c, err := app.FindCachedCollectionByNameOrId("traffic_daily")
			if err != nil {
				return err
			}
			record = core.NewRecord(c)
			record.Set("system", id)
			record.Set("iface", iface.Name)
			record.Set("day", bucket.Day)
			record.Set("quality", "normal")
		} else {
			if record.GetString("quality") == "suspected_reset" {
				continue
			}
			if bucket.RxBytes < uint64(record.GetInt64("rx_bytes")) || bucket.TxBytes < uint64(record.GetInt64("tx_bytes")) {
				record.Set("quality", "suspected_reset")
				source.Set("quality", "suspected_reset")
				source.Set("error_code", "counter_decreased")
				if err := app.SaveWithContext(ctx, record); err != nil {
					return err
				}
				continue
			}
			if bucket.RxBytes == uint64(record.GetInt64("rx_bytes")) && bucket.TxBytes == uint64(record.GetInt64("tx_bytes")) {
				continue
			}
		}
		record.Set("rx_bytes", bucket.RxBytes)
		record.Set("tx_bytes", bucket.TxBytes)
		record.Set("observed_at", snapshot.ObservedAt)
		if err := app.SaveWithContext(ctx, record); err != nil {
			return err
		}
	}
	// Use indexed min/max so retained Hub history is independent of vnStat's
	// local rolling window; never replace these with just the incoming window.
	var bounds struct {
		First string `db:"first"`
		Last  string `db:"last"`
	}
	if err := app.DB().NewQuery("SELECT COALESCE(MIN(day),'') AS first, COALESCE(MAX(day),'') AS last FROM traffic_daily WHERE system={:system} AND iface={:iface}").Bind(dbx.Params{"system": id, "iface": iface.Name}).WithContext(ctx).One(&bounds); err != nil {
		return err
	}
	source.Set("first_available_day", bounds.First)
	source.Set("last_available_day", bounds.Last)
	if len(iface.Days) > 0 {
		source.Set("last_success_at", snapshot.ObservedAt)
	}
	return app.SaveWithContext(ctx, source)
}
