//go:build testing

package systems

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestNativeTrafficAutomaticallyChoosesOneUplinkAndPersistsNewPartialFlag(t *testing.T) {
	sys, app := newTestSystemWithHub(t)
	snapshot := trafficTestSnapshot()
	snapshot.Interfaces[0].SourceID = "native-ledger-id"
	require.NoError(t, SaveTrafficSnapshot(t.Context(), app, sys.Id, snapshot))
	require.Equal(t, "eth0", trafficSettings(t, app, sys.Id).GetString("iface"))
	rows := trafficRecords(t, app, "traffic_daily", sys.Id)
	require.Equal(t, "normal", rows[29].GetString("quality"))
	snapshot.ObservedAt++
	snapshot.Interfaces[0].SourceUpdatedAt++
	snapshot.Interfaces[0].Days[29].Partial = true
	require.NoError(t, SaveTrafficSnapshot(t.Context(), app, sys.Id, snapshot))
	rows = trafficRecords(t, app, "traffic_daily", sys.Id)
	require.Equal(t, "partial", rows[29].GetString("quality"))
	require.EqualValues(t, 100, rows[29].GetInt64("rx_bytes"))
	// An explicit opt-out survives later Agent observations.
	settings := trafficSettings(t, app, sys.Id)
	settings.Set("enabled", false)
	settings.Set("iface", "")
	require.NoError(t, app.Save(settings))
	snapshot.ObservedAt++
	snapshot.Interfaces[0].SourceUpdatedAt++
	require.NoError(t, SaveTrafficSnapshot(t.Context(), app, sys.Id, snapshot))
	require.False(t, trafficSettings(t, app, sys.Id).GetBool("enabled"))
	require.Empty(t, trafficSettings(t, app, sys.Id).GetString("iface"))
}
