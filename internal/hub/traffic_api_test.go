//go:build testing

package hub_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/CodeAIX/vpsmonitor/internal/entities/traffic"
	"github.com/CodeAIX/vpsmonitor/internal/hub/systems"
	beszelTests "github.com/CodeAIX/vpsmonitor/internal/tests"
	"github.com/pocketbase/pocketbase/core"
	"github.com/stretchr/testify/require"
)

func trafficAPIRequest(t *testing.T, handler http.Handler, method, path, token, body string) (int, map[string]any) {
	t.Helper()
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", token)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var result map[string]any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result), response.Body.String())
	return response.Code, result
}

func seedTrafficAPI(t *testing.T, app core.App, id string) {
	t.Helper()
	now := time.Now().UTC().Add(-2 * time.Minute)
	snapshot := &traffic.Snapshot{SchemaVersion: 1, Status: traffic.OK, ObservedAt: now.Unix(), Timebase: "utc", UTCValidFromDay: now.AddDate(0, 0, -60).Format(time.DateOnly), RefreshSeconds: 300, SaveIntervalSeconds: 300, CollectorVersion: "2.13", Interfaces: []traffic.InterfaceDaily{{Name: "eth0", SourceCreatedAt: now.AddDate(0, 0, -60).Unix(), SourceUpdatedAt: now.Unix(), Days: []traffic.DailyBucket{{Day: now.Format(time.DateOnly), RxBytes: 100, TxBytes: 200}}}}}
	require.NoError(t, systems.SaveTrafficSnapshot(t.Context(), app, id, snapshot))
}

func TestTrafficAPIAuthorizationAndSettings(t *testing.T) {
	t.Setenv("SHARE_ALL_SYSTEMS", "false")
	t.Setenv("VPSMONITOR_HUB_SHARE_ALL_SYSTEMS", "false")
	app, handler := firstUserTestMux(t)
	t.Cleanup(app.Cleanup)
	owner, err := beszelTests.CreateUserWithRole(app, "traffic-owner@example.com", "password123", "user")
	require.NoError(t, err)
	other, err := beszelTests.CreateUserWithRole(app, "traffic-other@example.com", "password123", "user")
	require.NoError(t, err)
	readonly, err := beszelTests.CreateUserWithRole(app, "traffic-reader@example.com", "password123", "readonly")
	require.NoError(t, err)
	ownerToken, err := owner.NewAuthToken()
	require.NoError(t, err)
	otherToken, err := other.NewAuthToken()
	require.NoError(t, err)
	readerToken, err := readonly.NewAuthToken()
	require.NoError(t, err)
	record, err := beszelTests.CreateRecord(app, "systems", map[string]any{"name": "traffic-test", "host": "127.0.0.1", "port": "45876", "status": "paused", "users": []string{owner.Id, readonly.Id}})
	require.NoError(t, err)
	seedTrafficAPI(t, app, record.Id)
	base := "/api/vpsmonitor/traffic/"
	code, _ := trafficAPIRequest(t, handler, "GET", base+"overview", "", "")
	require.Equal(t, 401, code)
	code, _ = trafficAPIRequest(t, handler, "GET", base+"detail?system="+record.Id, otherToken, "")
	require.Equal(t, 404, code)
	code, view := trafficAPIRequest(t, handler, "GET", base+"detail?system="+record.Id, ownerToken, "")
	require.Equal(t, 200, code)
	require.Equal(t, "ok", view["status"])
	require.Equal(t, "eth0", view["iface"], "the sole native uplink is configured automatically")
	settingsPath := base + "settings?system=" + record.Id
	code, _ = trafficAPIRequest(t, handler, "PATCH", settingsPath, readerToken, `{"iface":"eth0"}`)
	require.Equal(t, 403, code)
	code, _ = trafficAPIRequest(t, handler, "PATCH", settingsPath, otherToken, `{"iface":"eth0"}`)
	require.Equal(t, 404, code)
	code, view = trafficAPIRequest(t, handler, "PATCH", settingsPath, ownerToken, `{"iface":"eth0","quota_bytes":1000,"count_mode":"rx_tx","billing_day":1,"billing_timezone":"UTC","enabled":true}`)
	require.Equal(t, 200, code)
	require.Equal(t, float64(300), view["used_bytes"])
	require.Equal(t, float64(30), view["percent"])
	code, detail := trafficAPIRequest(t, handler, "GET", base+"detail?system="+record.Id, ownerToken, "")
	require.Equal(t, 200, code)
	days := detail["days"].([]any)
	require.Len(t, days, 30)
	require.Nil(t, days[0].(map[string]any)["rx_bytes"], "missing dates must not become zero")
	// Observing a stopped daemon's unchanged DB does not refresh the source.
	source, err := app.FindFirstRecordByFilter("traffic_sources", "system={:id}", map[string]any{"id": record.Id})
	require.NoError(t, err)
	source.Set("source_updated_at", time.Now().Unix()-3600)
	require.NoError(t, app.Save(source))
	code, detail = trafficAPIRequest(t, handler, "GET", base+"detail?system="+record.Id, ownerToken, "")
	require.Equal(t, 200, code)
	require.Equal(t, "stale", detail["freshness"])
	source.Set("refresh_seconds", 3600)
	require.NoError(t, app.Save(source))
	code, detail = trafficAPIRequest(t, handler, "GET", base+"detail?system="+record.Id, ownerToken, "")
	require.Equal(t, 200, code)
	require.Equal(t, "fresh", detail["freshness"], "1h refresh must not use a fixed 15m threshold")
	daily, err := app.FindFirstRecordByFilter("traffic_daily", "system={:id}", map[string]any{"id": record.Id})
	require.NoError(t, err)
	rawPath := "/api/collections/traffic_daily/records/" + daily.Id
	code, _ = trafficAPIRequest(t, handler, "GET", rawPath, otherToken, "")
	require.Equal(t, 404, code)
	code, _ = trafficAPIRequest(t, handler, "PATCH", rawPath, ownerToken, `{"rx_bytes":999}`)
	require.Equal(t, 403, code)
	code, _ = trafficAPIRequest(t, handler, "DELETE", rawPath, ownerToken, "")
	require.Equal(t, 403, code)
	code, overview := trafficAPIRequest(t, handler, "GET", base+"overview", ownerToken, "")
	require.Equal(t, 200, code)
	require.Len(t, overview["items"].([]any), 1)
	code, overview = trafficAPIRequest(t, handler, "GET", base+"overview", otherToken, "")
	require.Equal(t, 200, code)
	require.Empty(t, overview["items"])
	code, _ = trafficAPIRequest(t, handler, "GET", base+"detail?system="+record.Id, readerToken, "")
	require.Equal(t, 200, code)
	for _, body := range []string{`{"iface":"not-owned"}`, `{"quota_bytes":-1}`, `{"quota_bytes":9007199254740992}`, `{"billing_day":31}`, `{"billing_timezone":"Asia/Shanghai"}`, `{"count_mode":"rx"}`, `{"collector_status":"ok"}`, `{"system":"somebody-else"}`, `{"rx_bytes":999}`, `{} {}`} {
		code, _ = trafficAPIRequest(t, handler, "PATCH", settingsPath, ownerToken, body)
		require.Equal(t, 400, code, body)
	}
	for _, query := range []string{"&from=2026-02-30", "&from=2020-01-01&to=2026-01-01", "&from=2026-11-06&to=2026-11-05"} {
		code, _ = trafficAPIRequest(t, handler, "GET", base+"detail?system="+record.Id+query, ownerToken, "")
		require.Equal(t, 400, code)
	}
	// Share-all follows the upstream systems write semantics. Readonly still cannot write.
	t.Setenv("SHARE_ALL_SYSTEMS", "true")
	t.Setenv("VPSMONITOR_HUB_SHARE_ALL_SYSTEMS", "true")
	code, _ = trafficAPIRequest(t, handler, "GET", base+"detail?system="+record.Id, otherToken, "")
	require.Equal(t, 200, code)
	code, _ = trafficAPIRequest(t, handler, "PATCH", settingsPath, otherToken, `{"billing_day":15}`)
	require.Equal(t, 200, code)
	code, _ = trafficAPIRequest(t, handler, "PATCH", settingsPath, readerToken, `{"billing_day":15}`)
	require.Equal(t, 403, code)
}

func TestTrafficCollectionsRejectDirectClientWrites(t *testing.T) {
	app, handler := firstUserTestMux(t)
	t.Cleanup(app.Cleanup)
	user, err := beszelTests.CreateUserWithRole(app, "traffic-direct@example.com", "password123", "admin")
	require.NoError(t, err)
	token, err := user.NewAuthToken()
	require.NoError(t, err)
	for _, name := range []string{"traffic_settings", "traffic_sources", "traffic_daily"} {
		code, _ := trafficAPIRequest(t, handler, "POST", "/api/collections/"+name+"/records", token, `{"system":"forged","rx_bytes":999}`)
		require.Equal(t, 403, code, name)
	}
}

func TestTrafficSettingsBeforeFirstSample(t *testing.T) {
	app, handler := firstUserTestMux(t)
	t.Cleanup(app.Cleanup)
	user, err := beszelTests.CreateUserWithRole(app, "traffic-empty@example.com", "password123", "user")
	require.NoError(t, err)
	token, err := user.NewAuthToken()
	require.NoError(t, err)
	record, err := beszelTests.CreateRecord(app, "systems", map[string]any{"name": "unconfigured", "host": "127.0.0.1", "port": "45876", "status": "paused", "users": []string{user.Id}})
	require.NoError(t, err)
	code, view := trafficAPIRequest(t, handler, "PATCH", "/api/vpsmonitor/traffic/settings?system="+record.Id, token, `{"enabled":true,"quota_bytes":0}`)
	require.Equal(t, 200, code)
	require.Equal(t, "not_enabled", view["status"])
	require.Nil(t, view["used_bytes"])
	require.Empty(t, view["sources"])
}
