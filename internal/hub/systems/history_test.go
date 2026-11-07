//go:build testing

package systems

import (
	"errors"
	"testing"
	"time"

	"github.com/CodeAIX/vpsmonitor/internal/entities/container"
	"github.com/CodeAIX/vpsmonitor/internal/entities/system"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/stretchr/testify/require"
)

func historyFixture(start time.Time, count int) (system.HistoryInfo, []system.HistorySample) {
	samples := make([]system.HistorySample, 0, count)
	for n := 0; n < count; n++ {
		samples = append(samples, system.HistorySample{At: start.Add(time.Duration(n) * time.Minute).UnixMilli(), Stats: system.Stats{Cpu: 42, MemPct: 25, CpuCoresUsage: system.Uint8Slice{42, 43}, Bandwidth: [2]uint64{1000, 2000}}, Containers: []*container.Stats{{Name: "sample", Cpu: 3, Mem: 128, Bandwidth: [2]uint64{100, 200}}}})
	}
	return system.HistoryInfo{Version: 1, RetentionSeconds: int64(system.HistoryRetention / time.Second), Oldest: samples[0].At, Newest: samples[len(samples)-1].At, Samples: len(samples), Bytes: int64(len(samples) * 512)}, samples
}

func ingestHistory(t *testing.T, app core.App, id string, info system.HistoryInfo, samples []system.HistorySample, now time.Time) {
	t.Helper()
	cursor := int64(0)
	for offset := 0; offset < len(samples); offset += system.HistoryMaxPageSamples {
		end := min(offset+system.HistoryMaxPageSamples, len(samples))
		page := system.HistoryPage{Version: 1, Samples: samples[offset:end], Next: samples[end-1].At, More: end < len(samples), Info: info}
		req := system.HistoryRequest{After: cursor, Before: info.Newest, Limit: system.HistoryMaxPageSamples}
		require.NoError(t, SaveHistoryPage(t.Context(), app, id, info, req, page, now))
		cursor = page.Next
	}
}

func TestHistoryReplayAllChartTiersIdempotencyAndNoLiveSideEffects(t *testing.T) {
	sys, app := newTestSystemWithHub(t)
	now := time.Now().UTC().Truncate(time.Minute)
	r, err := app.FindRecordById("systems", sys.Id)
	require.NoError(t, err)
	r.Set("status", "paused")
	r.Set("info", system.Info{Cpu: 7, AgentVersion: "sentinel-live-version"})
	require.NoError(t, app.SaveNoValidate(r))
	// Old recovered samples must be available in the week/month tiers even
	// though upstream's 1m/10m/20m rollup jobs no longer visit those dates.
	start := now.Add(-6 * 24 * time.Hour).Truncate(8 * time.Hour).Add(15 * time.Second)
	info, samples := historyFixture(start, 480)
	ingestHistory(t, app, sys.Id, info, samples, now)
	var oldWeek []*core.Record
	require.NoError(t, app.RecordQuery("agent_history_records").AndWhere(dbx.HashExp{"system": sys.Id, "type": "120m"}).OrderBy("stamp").All(&oldWeek))
	require.Len(t, oldWeek, 4)
	for _, row := range oldWeek {
		require.EqualValues(t, 120, row.GetInt("samples"))
		var s system.Stats
		require.NoError(t, row.UnmarshalJSONField("stats", &s))
		require.Equal(t, float64(42), s.Cpu)
	}
	month, err := app.FindFirstRecordByFilter("agent_history_records", "system={:id} && type='480m'", dbx.Params{"id": sys.Id})
	require.NoError(t, err)
	require.EqualValues(t, 480, month.GetInt("samples"))
	var cs []container.Stats
	require.NoError(t, month.UnmarshalJSONField("containers", &cs))
	require.Len(t, cs, 1)
	require.Equal(t, float64(3), cs[0].Cpu)
	before, err := app.CountRecords("agent_history_records")
	require.NoError(t, err)
	ingestHistory(t, app, sys.Id, info, samples, now)
	after, err := app.CountRecords("agent_history_records")
	require.NoError(t, err)
	require.Equal(t, before, after)
	// A recent interval builds the smaller graph resolutions as well.
	info, samples = historyFixture(now.Add(-4*time.Hour).Truncate(20*time.Minute).Add(15*time.Second), 20)
	// New metadata includes the full actual journal range; its cursor is per Hub.
	info.Oldest = start.UnixMilli()
	info.Samples += 480
	ingestHistory(t, app, sys.Id, info, samples, now)
	for _, kind := range []string{"1m", "10m", "20m", "120m", "480m"} {
		n, err := app.CountRecords("agent_history_records", dbx.HashExp{"system": sys.Id, "type": kind})
		require.NoError(t, err)
		require.Greater(t, n, int64(0), kind)
	}
	for _, name := range []string{"system_stats", "container_stats", "alerts_history"} {
		n, err := app.CountRecords(name)
		require.NoError(t, err)
		require.EqualValues(t, 0, n, name)
	}
	r, err = app.FindRecordById("systems", sys.Id)
	require.NoError(t, err)
	require.Equal(t, "paused", r.GetString("status"))
	var live system.Info
	require.NoError(t, r.UnmarshalJSONField("info", &live))
	require.Equal(t, float64(7), live.Cpu)
	require.Equal(t, "sentinel-live-version", live.AgentVersion)
	// After raw retention expires, the month aggregate still survives cleanup.
	require.NoError(t, CleanupHistory(app, now.Add(8*24*time.Hour)))
	n, err := app.CountRecords("agent_history_records", dbx.HashExp{"system": sys.Id, "type": "1m"})
	require.NoError(t, err)
	require.EqualValues(t, 0, n)
	n, err = app.CountRecords("agent_history_records", dbx.HashExp{"system": sys.Id, "type": "480m"})
	require.NoError(t, err)
	require.Greater(t, n, int64(0))
}

func TestHistoryCursorAndRawRowsRollbackTogether(t *testing.T) {
	sys, app := newTestSystemWithHub(t)
	now := time.Now().UTC()
	info, samples := historyFixture(now.Add(-10*time.Minute), 3)
	page := system.HistoryPage{Version: 1, Samples: samples, Next: info.Newest, Info: info}
	req := system.HistoryRequest{After: 0, Before: info.Newest, Limit: 128}
	hook := app.OnRecordCreate("agent_history_records").BindFunc(func(e *core.RecordEvent) error {
		if e.Record.GetString("type") != "1m" {
			return errors.New("simulated aggregate storage failure")
		}
		return e.Next()
	})
	require.Error(t, SaveHistoryPage(t.Context(), app, sys.Id, info, req, page, now))
	app.OnRecordCreate("agent_history_records").Unbind(hook)
	n, err := app.CountRecords("agent_history_records")
	require.NoError(t, err)
	require.EqualValues(t, 0, n)
	n, err = app.CountRecords("agent_history_state")
	require.NoError(t, err)
	require.EqualValues(t, 0, n)
	require.NoError(t, SaveHistoryPage(t.Context(), app, sys.Id, info, req, page, now))
	state, err := historyState(app, sys.Id)
	require.NoError(t, err)
	require.Equal(t, info.Newest, state.GetInt64("cursor"))
	page.Next = info.Newest + 1
	require.Error(t, SaveHistoryPage(t.Context(), app, sys.Id, info, req, page, now))
}

func TestHistoryLostWindowIsReportedWithoutCreatingZeros(t *testing.T) {
	sys, app := newTestSystemWithHub(t)
	now := time.Now().UTC()
	info, samples := historyFixture(now.Add(-time.Hour), 1)
	ingestHistory(t, app, sys.Id, info, samples, now)
	next, more := historyFixture(now.Add(-time.Minute), 1)
	page := system.HistoryPage{Version: 1, Samples: more, Next: next.Newest, Info: next}
	req := system.HistoryRequest{After: info.Newest, Before: next.Newest, Limit: 128}
	require.NoError(t, SaveHistoryPage(t.Context(), app, sys.Id, next, req, page, now))
	state, err := historyState(app, sys.Id)
	require.NoError(t, err)
	require.Greater(t, state.GetInt64("gap_to"), state.GetInt64("gap_from"))
	n, err := app.CountRecords("agent_history_records", dbx.HashExp{"system": sys.Id, "type": "1m"})
	require.NoError(t, err)
	require.EqualValues(t, 2, n)
}

func TestHistoryMigrationRoundTripPreservesOriginalRecords(t *testing.T) {
	sys, app := newTestSystemWithHub(t)
	_, err := sys.createRecords(&system.CombinedData{Stats: system.Stats{Cpu: 12}, Info: system.Info{Uptime: 42}})
	require.NoError(t, err)
	var list core.MigrationsList
	for _, migration := range core.AppMigrations.Items() {
		if migration.File == "1793952000_agent_history.go" {
			list.Add(migration)
		}
	}
	require.Len(t, list.Items(), 1)
	runner := core.NewMigrationsRunner(app, list)
	for range 2 {
		reverted, err := runner.Down(1)
		require.NoError(t, err)
		require.Equal(t, []string{"1793952000_agent_history.go"}, reverted)
		_, err = app.FindCollectionByNameOrId("agent_history_records")
		require.Error(t, err)
		for _, name := range []string{"systems", "system_stats"} {
			n, err := app.CountRecords(name)
			require.NoError(t, err)
			require.EqualValues(t, 1, n)
		}
		_, err = runner.Up()
		require.NoError(t, err)
		n, err := app.CountRecords("agent_history_records")
		require.NoError(t, err)
		require.Zero(t, n)
	}
}
