//go:build testing

package systems

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/CodeAIX/vpsmonitor/internal/entities/system"
	"github.com/CodeAIX/vpsmonitor/internal/entities/traffic"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/stretchr/testify/require"
)

// One fixture ledger has one immutable creation identity across observations.
var trafficFixtureCreatedAt = time.Now().UTC().AddDate(0, 0, -60).Unix()

func trafficTestSnapshot() *traffic.Snapshot {
	now := time.Now().UTC().Add(-2 * time.Minute)
	s := &traffic.Snapshot{SchemaVersion: 1, Status: traffic.OK, ObservedAt: now.Unix(), Timebase: "utc", UTCValidFromDay: now.AddDate(0, 0, -60).Format(time.DateOnly), RefreshSeconds: 300, SaveIntervalSeconds: 300, CollectorVersion: "2.13"}
	iface := traffic.InterfaceDaily{Name: "eth0", SourceCreatedAt: trafficFixtureCreatedAt, SourceUpdatedAt: now.Unix()}
	for day := 29; day >= 0; day-- {
		iface.Days = append(iface.Days, traffic.DailyBucket{Day: now.AddDate(0, 0, -day).Format(time.DateOnly), RxBytes: 100, TxBytes: 200})
	}
	s.Interfaces = []traffic.InterfaceDaily{iface}
	return s
}

func trafficRecords(t *testing.T, app core.App, name, id string) []*core.Record {
	t.Helper()
	var rows []*core.Record
	require.NoError(t, app.RecordQuery(name).AndWhere(dbx.HashExp{"system": id}).OrderBy("day").All(&rows))
	return rows
}

func trafficSettings(t *testing.T, app core.App, id string) *core.Record {
	t.Helper()
	record, err := app.FindFirstRecordByFilter("traffic_settings", "system={:system}", dbx.Params{"system": id})
	require.NoError(t, err)
	return record
}

func TestTrafficPersistenceImportRepeatUpdateAndOrder(t *testing.T) {
	sys, app := newTestSystemWithHub(t)
	snapshot := trafficTestSnapshot()
	require.NoError(t, SaveTrafficSnapshot(t.Context(), app, sys.Id, snapshot))
	require.Len(t, trafficRecords(t, app, "traffic_daily", sys.Id), 30)
	for range 10 {
		require.NoError(t, SaveTrafficSnapshot(t.Context(), app, sys.Id, snapshot))
	}
	rows := trafficRecords(t, app, "traffic_daily", sys.Id)
	require.Len(t, rows, 30)
	var total int64
	for _, row := range rows {
		total += row.GetInt64("rx_bytes") + row.GetInt64("tx_bytes")
	}
	require.Equal(t, int64(9000), total)
	snapshot.ObservedAt++
	snapshot.Interfaces[0].SourceUpdatedAt++
	snapshot.Interfaces[0].Days[29].TxBytes = 240
	require.NoError(t, SaveTrafficSnapshot(t.Context(), app, sys.Id, snapshot))
	rows = trafficRecords(t, app, "traffic_daily", sys.Id)
	require.Equal(t, int64(240), rows[29].GetInt64("tx_bytes"))
	// Old data and old error states cannot overwrite newer state or counters.
	old := trafficTestSnapshot()
	old.ObservedAt = snapshot.ObservedAt - 1
	require.NoError(t, SaveTrafficSnapshot(t.Context(), app, sys.Id, old))
	old.Status = traffic.Timeout
	old.ErrorCode = traffic.Timeout
	old.Interfaces = nil
	require.NoError(t, SaveTrafficSnapshot(t.Context(), app, sys.Id, old))
	require.Equal(t, traffic.OK, trafficSettings(t, app, sys.Id).GetString("collector_status"))
	require.Equal(t, int64(240), trafficRecords(t, app, "traffic_daily", sys.Id)[29].GetInt64("tx_bytes"))
	// Identical timestamps with different data are rejected and diagnosed.
	snapshot.Interfaces[0].Days[29].TxBytes = 250
	require.NoError(t, SaveTrafficSnapshot(t.Context(), app, sys.Id, snapshot))
	require.Equal(t, "snapshot_conflict", trafficSettings(t, app, sys.Id).GetString("collector_error_code"))
	require.Equal(t, int64(240), trafficRecords(t, app, "traffic_daily", sys.Id)[29].GetInt64("tx_bytes"))
}

func TestTrafficPersistenceResetIsSticky(t *testing.T) {
	sys, app := newTestSystemWithHub(t)
	snapshot := trafficTestSnapshot()
	require.NoError(t, SaveTrafficSnapshot(t.Context(), app, sys.Id, snapshot))
	snapshot.ObservedAt++
	snapshot.Interfaces[0].SourceUpdatedAt++
	snapshot.Interfaces[0].Days[29].TxBytes = 10
	require.NoError(t, SaveTrafficSnapshot(t.Context(), app, sys.Id, snapshot))
	row := trafficRecords(t, app, "traffic_daily", sys.Id)[29]
	require.Equal(t, "suspected_reset", row.GetString("quality"))
	require.Equal(t, int64(200), row.GetInt64("tx_bytes"))
	snapshot.ObservedAt++
	snapshot.Interfaces[0].SourceUpdatedAt++
	snapshot.Interfaces[0].Days[29].TxBytes = 999
	require.NoError(t, SaveTrafficSnapshot(t.Context(), app, sys.Id, snapshot))
	row = trafficRecords(t, app, "traffic_daily", sys.Id)[29]
	require.Equal(t, int64(200), row.GetInt64("tx_bytes"))
	source, err := app.FindFirstRecordByFilter("traffic_sources", "system={:system}", dbx.Params{"system": sys.Id})
	require.NoError(t, err)
	require.Equal(t, "suspected_reset", source.GetString("quality"))
	require.Equal(t, "counter_decreased", source.GetString("error_code"))
}

func TestTrafficPersistenceSourceRecreated(t *testing.T) {
	sys, app := newTestSystemWithHub(t)
	snapshot := trafficTestSnapshot()
	require.NoError(t, SaveTrafficSnapshot(t.Context(), app, sys.Id, snapshot))
	originalUTCFrom := snapshot.UTCValidFromDay
	snapshot.UTCValidFromDay = time.Now().UTC().Format(time.DateOnly)
	snapshot.Interfaces[0].Days = snapshot.Interfaces[0].Days[29:]
	snapshot.Interfaces[0].SourceCreatedAt++
	for range 2 {
		snapshot.ObservedAt++
		snapshot.Interfaces[0].SourceUpdatedAt++
		snapshot.Interfaces[0].Days[0].TxBytes = 1000
		require.NoError(t, SaveTrafficSnapshot(t.Context(), app, sys.Id, snapshot))
	}
	require.Equal(t, int64(200), trafficRecords(t, app, "traffic_daily", sys.Id)[29].GetInt64("tx_bytes"))
	source, err := app.FindFirstRecordByFilter("traffic_sources", "system={:system}", dbx.Params{"system": sys.Id})
	require.NoError(t, err)
	require.Equal(t, "source_recreated", source.GetString("error_code"))
	require.Equal(t, originalUTCFrom, source.GetString("utc_valid_from_day"), "a recreated DB must not hide retained history by changing its boundary")
	require.Equal(t, "suspected_reset", source.GetString("quality"))
}

func TestTrafficPersistenceFailureWithoutFakeInterface(t *testing.T) {
	sys, app := newTestSystemWithHub(t)
	snapshot := trafficTestSnapshot()
	snapshot.Status = traffic.Missing
	snapshot.ErrorCode = traffic.Missing
	snapshot.Interfaces = nil
	require.NoError(t, SaveTrafficSnapshot(t.Context(), app, sys.Id, snapshot))
	settings := trafficSettings(t, app, sys.Id)
	require.Empty(t, settings.GetString("iface"))
	require.Zero(t, settings.GetInt64("collector_last_success_at"))
	count, err := app.CountRecords("traffic_sources", dbx.HashExp{"system": sys.Id})
	require.NoError(t, err)
	require.Zero(t, count)
	snapshot = trafficTestSnapshot()
	snapshot.ObservedAt++
	require.NoError(t, SaveTrafficSnapshot(t.Context(), app, sys.Id, snapshot))
	good := snapshot.ObservedAt
	snapshot.ObservedAt++
	snapshot.Status = traffic.Timeout
	snapshot.ErrorCode = traffic.Timeout
	snapshot.Interfaces = nil
	require.NoError(t, SaveTrafficSnapshot(t.Context(), app, sys.Id, snapshot))
	require.Equal(t, good, trafficSettings(t, app, sys.Id).GetInt64("collector_last_success_at"))
	require.Len(t, trafficRecords(t, app, "traffic_daily", sys.Id), 30)
}

func TestTrafficPersistenceSafeIntegerCascadeAndInvalidData(t *testing.T) {
	sys, app := newTestSystemWithHub(t)
	snapshot := trafficTestSnapshot()
	snapshot.Interfaces[0].Days = []traffic.DailyBucket{{Day: time.Now().UTC().Format(time.DateOnly), RxBytes: traffic.MaxSafeBytes}}
	require.NoError(t, SaveTrafficSnapshot(t.Context(), app, sys.Id, snapshot))
	require.Equal(t, int64(traffic.MaxSafeBytes), trafficRecords(t, app, "traffic_daily", sys.Id)[0].GetInt64("rx_bytes"))
	snapshot.ObservedAt++
	snapshot.Interfaces[0].Days[0].RxBytes++
	require.Error(t, SaveTrafficSnapshot(t.Context(), app, sys.Id, snapshot))
	require.Equal(t, int64(traffic.MaxSafeBytes), trafficRecords(t, app, "traffic_daily", sys.Id)[0].GetInt64("rx_bytes"))
	record, err := app.FindRecordById("systems", sys.Id)
	require.NoError(t, err)
	require.NoError(t, app.Delete(record))
	for _, name := range []string{"traffic_sources", "traffic_daily", "traffic_settings"} {
		count, err := app.CountRecords(name, dbx.HashExp{"system": sys.Id})
		require.NoError(t, err)
		require.Zero(t, count)
	}
}

func TestTrafficFailureDoesNotRollbackMetrics(t *testing.T) {
	sys, app := newTestSystemWithHub(t)
	_, err := sys.createRecords(&system.CombinedData{Stats: system.Stats{Cpu: 12}, Info: system.Info{Uptime: 42}})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.Error(t, SaveTrafficSnapshot(ctx, app, sys.Id, trafficTestSnapshot()))
	count, err := app.CountRecords("system_stats", dbx.HashExp{"system": sys.Id})
	require.NoError(t, err)
	require.Equal(t, int64(1), count)
	count, err = app.CountRecords("traffic_settings", dbx.HashExp{"system": sys.Id})
	require.NoError(t, err)
	require.Zero(t, count)
}

func TestTrafficMigrationRoundTripPreservesOriginalRecords(t *testing.T) {
	sys, app := newTestSystemWithHub(t)
	_, err := sys.createRecords(&system.CombinedData{Stats: system.Stats{Cpu: 12}, Info: system.Info{Uptime: 42}})
	require.NoError(t, err)
	var list core.MigrationsList
	for _, migration := range core.AppMigrations.Items() {
		if migration.File == "1793937600_traffic.go" {
			list.Add(migration)
		}
	}
	require.Len(t, list.Items(), 1)
	runner := core.NewMigrationsRunner(app, list)
	reverted, err := runner.Down(1)
	require.NoError(t, err)
	require.Equal(t, []string{"1793937600_traffic.go"}, reverted)
	_, err = app.FindCollectionByNameOrId("traffic_daily")
	require.Error(t, err)
	// This temp DB now represents a populated pre-extension schema. Additive
	// migration, its down, and re-up must leave original records untouched.
	for range 2 {
		applied, err := runner.Up()
		require.NoError(t, err)
		require.Len(t, applied, 1)
		count, err := app.CountRecords("systems")
		require.NoError(t, err)
		require.Equal(t, int64(1), count)
		count, err = app.CountRecords("system_stats")
		require.NoError(t, err)
		require.Equal(t, int64(1), count)
		reverted, err = runner.Down(1)
		require.NoError(t, err)
		require.Len(t, reverted, 1)
	}
}

func TestTrafficWriterBoundsAndCoalesces(t *testing.T) {
	sys, app := newTestSystemWithHub(t)
	sys.manager.ctx, sys.manager.cancel = context.WithCancel(t.Context())
	w := &trafficWriter{manager: sys.manager, pending: make(map[string]*traffic.Snapshot)}
	entered, release := make(chan struct{}), make(chan struct{})
	app.OnRecordCreate("traffic_daily").BindFunc(func(e *core.RecordEvent) error {
		select {
		case <-entered:
		default:
			close(entered)
		}
		<-release
		return e.Next()
	})
	w.enqueue(sys.Id, trafficTestSnapshot())
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("writer did not start")
	}
	for i := range 100 {
		w.enqueue(fmt.Sprintf("queued-%d", i), trafficTestSnapshot())
	}
	newer := trafficTestSnapshot()
	newer.ObservedAt++
	w.enqueue("queued-0", newer)
	w.enqueue("queued-0", trafficTestSnapshot())
	w.mu.Lock()
	count := len(w.pending)
	accepted := w.pending["queued-0"].ObservedAt
	w.mu.Unlock()
	require.Equal(t, 50, count)
	require.Equal(t, newer.ObservedAt, accepted)
	sys.manager.cancel()
	close(release)
	w.stop()
	sys.manager.updaters.Wait()
}

func TestTrafficCatchupAfterHubOutage(t *testing.T) {
	sys, app := newTestSystemWithHub(t)
	// Recovery is the same ledger at a later observation time. Calling the
	// time-based fixture twice can change SourceCreatedAt on slower runners,
	// accidentally testing source replacement instead of offline catch-up.
	resumed := trafficTestSnapshot()
	old := *resumed
	old.Interfaces = append([]traffic.InterfaceDaily(nil), resumed.Interfaces...)
	old.ObservedAt -= 3 * 24 * 3600
	old.Interfaces[0].SourceUpdatedAt = old.ObservedAt
	old.Interfaces[0].Days = old.Interfaces[0].Days[:27]
	require.NoError(t, SaveTrafficSnapshot(t.Context(), app, sys.Id, &old))
	require.Len(t, trafficRecords(t, app, "traffic_daily", sys.Id), 27)
	require.NoError(t, SaveTrafficSnapshot(t.Context(), app, sys.Id, resumed))
	require.NoError(t, SaveTrafficSnapshot(t.Context(), app, sys.Id, resumed))
	rows := trafficRecords(t, app, "traffic_daily", sys.Id)
	require.Len(t, rows, 30)
	var total int64
	for _, row := range rows {
		total += row.GetInt64("rx_bytes") + row.GetInt64("tx_bytes")
	}
	require.Equal(t, int64(9000), total)
}
