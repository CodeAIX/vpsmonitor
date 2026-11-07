//go:build testing

package hub_test

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/CodeAIX/vpsmonitor/internal/entities/system"
	"github.com/CodeAIX/vpsmonitor/internal/hub/systems"
	beszelTests "github.com/CodeAIX/vpsmonitor/internal/tests"
	"github.com/stretchr/testify/require"
)

func TestHistoryAPIAuthorizationAndHistoricalCharts(t *testing.T) {
	t.Setenv("SHARE_ALL_SYSTEMS", "false")
	t.Setenv("VPSMONITOR_HUB_SHARE_ALL_SYSTEMS", "false")
	app, handler := firstUserTestMux(t)
	t.Cleanup(app.Cleanup)
	owner, err := beszelTests.CreateUserWithRole(app, "history-owner@example.com", "password123", "user")
	require.NoError(t, err)
	reader, err := beszelTests.CreateUserWithRole(app, "history-reader@example.com", "password123", "readonly")
	require.NoError(t, err)
	other, err := beszelTests.CreateUserWithRole(app, "history-other@example.com", "password123", "user")
	require.NoError(t, err)
	ownerToken, err := owner.NewAuthToken()
	require.NoError(t, err)
	readerToken, err := reader.NewAuthToken()
	require.NoError(t, err)
	otherToken, err := other.NewAuthToken()
	require.NoError(t, err)
	record, err := beszelTests.CreateRecord(app, "systems", map[string]any{"name": "history-api", "host": "127.0.0.1", "port": "9", "users": []string{owner.Id, reader.Id}})
	require.NoError(t, err)
	now := time.Now().UTC()
	at := now.Add(-4 * 24 * time.Hour).Truncate(2 * time.Hour).Add(15 * time.Second)
	info := system.HistoryInfo{Version: 1, RetentionSeconds: 604800, Oldest: at.UnixMilli(), Newest: at.UnixMilli(), Samples: 1, Bytes: 512}
	page := system.HistoryPage{Version: 1, Next: at.UnixMilli(), Info: info, Samples: []system.HistorySample{{At: at.UnixMilli(), Stats: system.Stats{Cpu: 42, MemPct: 25}}}}
	require.NoError(t, systems.SaveHistoryPage(t.Context(), app, record.Id, info, system.HistoryRequest{After: 0, Before: at.UnixMilli(), Limit: 128}, page, now))
	path := fmt.Sprintf("/api/vpsmonitor/history/stats?system=%s&type=120m&from=%d&to=%d", record.Id, now.Add(-7*24*time.Hour).UnixMilli(), now.UnixMilli()+1)
	for _, tc := range []struct {
		token string
		code  int
	}{{"", 401}, {otherToken, 404}, {ownerToken, 200}, {readerToken, 200}} {
		code, data := trafficAPIRequest(t, handler, "GET", path, tc.token, "")
		require.Equal(t, tc.code, code)
		if code == 200 {
			points := data["points"].([]any)
			require.Len(t, points, 1)
			point := points[0].(map[string]any)
			require.Equal(t, false, point["complete"])
			require.EqualValues(t, 42, point["stats"].(map[string]any)["cpu"])
			require.Equal(t, true, data["state"].(map[string]any)["supported"])
		}
	}
	code, _ := trafficAPIRequest(t, handler, "GET", fmt.Sprintf("/api/vpsmonitor/history/stats?system=%s&type=1m&from=0&to=%d", record.Id, now.UnixMilli()), ownerToken, "")
	require.Equal(t, 400, code)
	// Collection writes remain server-only, including for ordinary owners.
	body, _ := json.Marshal(map[string]any{"system": record.Id, "stamp": at.UnixMilli(), "type": "1m", "stats": map[string]any{"cpu": 0}})
	code, _ = trafficAPIRequest(t, handler, "POST", "/api/collections/agent_history_records/records", ownerToken, string(body))
	require.Equal(t, 403, code)
}
