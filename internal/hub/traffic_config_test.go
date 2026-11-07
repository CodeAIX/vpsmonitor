//go:build testing

package hub_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	beszelTests "github.com/CodeAIX/vpsmonitor/internal/tests"
	"github.com/pocketbase/pocketbase/core"
	"github.com/stretchr/testify/require"
)

func TestPortableTrafficConfig(t *testing.T) {
	app, handler := firstUserTestMux(t)
	t.Cleanup(app.Cleanup)
	admin, err := beszelTests.CreateUserWithRole(app, "portable-admin@example.com", "password123", "admin")
	require.NoError(t, err)
	token, err := admin.NewAuthToken()
	require.NoError(t, err)
	sys, err := beszelTests.CreateRecord(app, "systems", map[string]any{"name": "portable", "host": "127.0.0.20", "port": "9", "users": []string{admin.Id}})
	require.NoError(t, err)
	_, err = beszelTests.CreateRecord(app, "fingerprints", map[string]any{"system": sys.Id, "token": "portable-agent-token"})
	require.NoError(t, err)
	seedTrafficAPI(t, app, sys.Id)
	settings := func() *core.Record {
		r, err := app.FindFirstRecordByFilter("traffic_settings", "system={:id}", map[string]any{"id": sys.Id})
		require.NoError(t, err)
		return r
	}
	r := settings()
	r.Set("iface", "eth0")
	r.Set("quota_bytes", uint64(9007199254740991))
	r.Set("billing_day", 15)
	r.Set("count_mode", "rx_tx")
	r.Set("display_unit", "GiB")
	require.NoError(t, app.Save(r))
	collectorStatus, digest := r.GetString("collector_status"), r.GetString("payload_digest")
	var standalone, combined string
	t.Run("three export formats and no operational metadata", func(t *testing.T) {
		for _, format := range []string{"classic", "combined", "traffic"} {
			code, data := trafficAPIRequest(t, handler, "GET", "/api/vpsmonitor/config-yaml?format="+format, token, "")
			require.Equal(t, 200, code)
			yaml := data["config"].(string)
			require.NotContains(t, yaml, "collector_status")
			require.NotContains(t, yaml, "payload_digest")
			require.NotContains(t, yaml, "rx_bytes")
			switch format {
			case "classic":
				require.NotContains(t, yaml, "traffic:")
				require.Contains(t, yaml, "portable-agent-token")
			case "combined":
				require.Contains(t, yaml, "portable-agent-token")
				require.Contains(t, yaml, "quota_bytes: 9007199254740991")
				combined = yaml
			case "traffic":
				require.NotContains(t, yaml, "portable-agent-token")
				require.NotContains(t, yaml, "token:")
				require.NotContains(t, yaml, "users:")
				require.Contains(t, yaml, "vpsmonitor-plus-traffic")
				standalone = yaml
			}
		}
	})
	request := func(yaml string, applyTraffic, apply bool, id string) (int, map[string]any) {
		b, err := json.Marshal(map[string]any{"config": yaml, "apply_traffic": applyTraffic, "apply": apply, "preview_id": id})
		require.NoError(t, err)
		return trafficAPIRequest(t, handler, "POST", "/api/vpsmonitor/config-yaml/import", token, string(b))
	}
	modified := strings.Replace(standalone, "9007199254740991", "20000000000", 1)
	t.Run("default preserves settings; explicit confirmation updates whitelist only", func(t *testing.T) {
		code, plan := request(modified, false, false, "")
		require.Equal(t, 200, code)
		require.EqualValues(t, 0, plan["update"])
		require.EqualValues(t, 1, plan["skip"])
		code, _ = request(modified, false, true, plan["preview_id"].(string))
		require.Equal(t, 200, code)
		require.EqualValues(t, 9007199254740991, settings().GetInt64("quota_bytes"))
		code, plan = request(modified, true, false, "")
		require.Equal(t, 200, code)
		require.EqualValues(t, 1, plan["update"])
		previewRow := plan["rows"].([]any)[0].(map[string]any)
		require.EqualValues(t, 20000000000, previewRow["traffic"].(map[string]any)["quota_bytes"])
		require.EqualValues(t, 9007199254740991, previewRow["current_traffic"].(map[string]any)["quota_bytes"])
		code, plan = request(modified, true, true, plan["preview_id"].(string))
		require.Equal(t, 200, code)
		require.EqualValues(t, 1, plan["updated"])
		require.EqualValues(t, 0, plan["created"])
		r := settings()
		require.EqualValues(t, 20000000000, r.GetInt64("quota_bytes"))
		require.Equal(t, collectorStatus, r.GetString("collector_status"))
		require.Equal(t, digest, r.GetString("payload_digest"))
		n, err := app.CountRecords("traffic_daily")
		require.NoError(t, err)
		require.EqualValues(t, 1, n)
		fp, err := app.FindFirstRecordByFilter("fingerprints", "system={:id}", map[string]any{"id": sys.Id})
		require.NoError(t, err)
		require.Equal(t, "portable-agent-token", fp.GetString("token"))
	})
	t.Run("stale settings preview cannot overwrite intervening changes", func(t *testing.T) {
		code, plan := request(standalone, true, false, "")
		require.Equal(t, 200, code)
		r := settings()
		r.Set("quota_bytes", 30000000000)
		require.NoError(t, app.Save(r))
		code, _ = request(standalone, true, true, plan["preview_id"].(string))
		require.Equal(t, 409, code)
		require.EqualValues(t, 30000000000, settings().GetInt64("quota_bytes"))
	})
	t.Run("combined file configures new system before agent collection", func(t *testing.T) {
		yaml := strings.Replace(combined, "127.0.0.20", "127.0.0.21", 1)
		yaml = strings.Replace(yaml, "portable-agent-token", "different-agent-token", 1)
		code, plan := request(yaml, false, false, "")
		require.Equal(t, 200, code)
		require.EqualValues(t, 1, plan["add"])
		code, _ = request(yaml, false, true, plan["preview_id"].(string))
		require.Equal(t, 200, code)
		s, err := app.FindFirstRecordByFilter("systems", "host='127.0.0.21'")
		require.NoError(t, err)
		r, err := app.FindFirstRecordByFilter("traffic_settings", "system={:id}", map[string]any{"id": s.Id})
		require.NoError(t, err)
		require.EqualValues(t, 9007199254740991, r.GetInt64("quota_bytes"))
		require.Empty(t, r.GetString("collector_status"))
	})
	t.Run("traffic-only cannot create unmatched systems", func(t *testing.T) {
		yaml := strings.Replace(standalone, "127.0.0.20", "127.0.0.22", 1)
		code, plan := request(yaml, true, false, "")
		require.Equal(t, 200, code)
		require.EqualValues(t, 1, plan["errors"])
		code, _ = request(yaml, true, true, plan["preview_id"].(string))
		require.Equal(t, 409, code)
		n, err := app.CountRecords("systems")
		require.NoError(t, err)
		require.EqualValues(t, 2, n)
	})
	t.Run("strict bounds and unknown protected fields", func(t *testing.T) {
		for _, replacement := range []struct {
			old, new string
			code     int
		}{
			{"9007199254740991", "9007199254740992", 200},
			{"billing_day: 15", "billing_day: 29", 200},
			{"billing_timezone: UTC", "billing_timezone: Asia/Shanghai", 200},
			{"count_mode: rx_tx", "count_mode: both", 200},
			{"display_unit: GiB", "display_unit: TB", 200},
			{"iface: eth0", "iface: " + strings.Repeat("a", 65), 200},
			{"traffic_schema: 1", "traffic_schema: 2", 400},
			{"traffic:", "traffic:\n        collector_status: ok", 400},
		} {
			yaml := strings.Replace(standalone, replacement.old, replacement.new, 1)
			require.NotEqual(t, standalone, yaml)
			code, plan := request(yaml, true, false, "")
			require.Equal(t, replacement.code, code, fmt.Sprint(replacement))
			if code == 200 {
				require.EqualValues(t, 1, plan["errors"], fmt.Sprint(replacement))
			}
		}
	})
	t.Run("settings save failure rolls back new system and fingerprint", func(t *testing.T) {
		yaml := strings.Replace(combined, "127.0.0.20", "127.0.0.23", 1)
		yaml = strings.Replace(yaml, "portable-agent-token", "rollback-agent-token", 1)
		code, plan := request(yaml, true, false, "")
		require.Equal(t, 200, code)
		hook := app.OnRecordCreate("traffic_settings").BindFunc(func(e *core.RecordEvent) error { return errors.New("simulated settings save failure") })
		defer app.OnRecordCreate("traffic_settings").Unbind(hook)
		code, _ = request(yaml, true, true, plan["preview_id"].(string))
		require.Equal(t, 500, code)
		n, err := app.CountRecords("systems")
		require.NoError(t, err)
		require.EqualValues(t, 2, n)
		n, err = app.CountRecords("fingerprints")
		require.NoError(t, err)
		require.EqualValues(t, 2, n)
	})
	t.Run("ambiguous existing endpoints and repeated settings cannot target the wrong system", func(t *testing.T) {
		// Repeating a traffic assignment is an error rather than last-write-wins.
		start := strings.Index(standalone, "    - name:")
		require.GreaterOrEqual(t, start, 0)
		code, plan := request(standalone+standalone[start:], true, false, "")
		require.Equal(t, 200, code)
		require.EqualValues(t, 1, plan["errors"])
		_, err := beszelTests.CreateRecord(app, "systems", map[string]any{"name": "ambiguous", "host": "127.0.0.20", "port": "9", "users": []string{admin.Id}})
		require.NoError(t, err)
		code, plan = request(standalone, true, false, "")
		require.Equal(t, 200, code)
		require.EqualValues(t, 1, plan["errors"])
		code, _ = request(standalone, true, true, plan["preview_id"].(string))
		require.Equal(t, 409, code)
	})
}
