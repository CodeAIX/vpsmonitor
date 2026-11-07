//go:build testing

package hub_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	beszelTests "github.com/CodeAIX/vpsmonitor/internal/tests"
	"github.com/pocketbase/pocketbase/core"
	"github.com/stretchr/testify/require"
)

func TestSystemImport(t *testing.T) {
	app, handler := firstUserTestMux(t)
	t.Cleanup(app.Cleanup)
	admin, err := beszelTests.CreateUserWithRole(app, "import-admin@example.com", "password123", "admin")
	require.NoError(t, err)
	other, err := beszelTests.CreateUserWithRole(app, "import-owner@example.com", "password123", "user")
	require.NoError(t, err)
	reader, err := beszelTests.CreateUserWithRole(app, "import-reader@example.com", "password123", "readonly")
	require.NoError(t, err)
	adminToken, err := admin.NewAuthToken()
	require.NoError(t, err)
	otherToken, err := other.NewAuthToken()
	require.NoError(t, err)
	readerToken, err := reader.NewAuthToken()
	require.NoError(t, err)
	existing, err := beszelTests.CreateRecord(app, "systems", map[string]any{"name": "keep me", "host": "127.0.0.1", "port": "9", "users": []string{admin.Id}})
	require.NoError(t, err)
	_, err = beszelTests.CreateRecord(app, "fingerprints", map[string]any{"system": existing.Id, "token": "keep-token-original", "fingerprint": "keep-fingerprint"})
	require.NoError(t, err)
	seedTrafficAPI(t, app, existing.Id)
	const path = "/api/vpsmonitor/config-yaml/import"
	request := func(token, yaml, mode string, apply bool, preview string) (int, map[string]any) {
		body, err := json.Marshal(map[string]any{"config": yaml, "user_mode": mode, "apply": apply, "preview_id": preview})
		require.NoError(t, err)
		return trafficAPIRequest(t, handler, "POST", path, token, string(body))
	}
	count := func() int64 { n, err := app.CountRecords("systems"); require.NoError(t, err); return n }
	yaml := `systems:
  - name: renamed duplicate
    host: 127.0.0.1
    port: 9
    token: do-not-overwrite
  - name: imported
    host: 127.0.0.2
    port: 9
    token: original-agent-token
    users: [missing@example.com]
  - name: generated
    host: 127.0.0.3
    port: 9
`
	t.Run("authorization", func(t *testing.T) {
		for _, tc := range []struct {
			token string
			code  int
		}{{"", 401}, {otherToken, 403}, {readerToken, 403}} {
			code, _ := request(tc.token, yaml, "current", false, "")
			require.Equal(t, tc.code, code)
			code, _ = request(tc.token, yaml, "current", true, "")
			require.Equal(t, tc.code, code)
		}
		require.EqualValues(t, 1, count())
	})
	t.Run("preview apply preserve and retry", func(t *testing.T) {
		code, plan := request(adminToken, yaml, "current", false, "")
		require.Equal(t, http.StatusOK, code)
		require.EqualValues(t, 2, plan["add"])
		require.EqualValues(t, 1, plan["skip"])
		require.EqualValues(t, 0, plan["errors"])
		require.EqualValues(t, 1, count(), "preview cannot write")
		encoded, _ := json.Marshal(plan)
		require.NotContains(t, string(encoded), "original-agent-token")
		code, _ = request(adminToken, yaml, "current", true, "")
		require.Equal(t, 409, code, "confirmation requires preview")
		code, applied := request(adminToken, yaml, "current", true, plan["preview_id"].(string))
		require.Equal(t, 200, code)
		require.EqualValues(t, 2, applied["created"])
		require.EqualValues(t, 3, count())
		fp, err := app.FindFirstRecordByFilter("fingerprints", "system={:id}", map[string]any{"id": existing.Id})
		require.NoError(t, err)
		require.Equal(t, "keep-token-original", fp.GetString("token"))
		require.Equal(t, "keep-fingerprint", fp.GetString("fingerprint"))
		kept, err := app.FindRecordById("systems", existing.Id)
		require.NoError(t, err)
		require.Equal(t, "keep me", kept.GetString("name"))
		n, err := app.CountRecords("traffic_daily")
		require.NoError(t, err)
		require.EqualValues(t, 1, n, "existing traffic survives")
		imported, err := app.FindFirstRecordByFilter("systems", "name='imported'")
		require.NoError(t, err)
		require.Equal(t, []string{admin.Id}, imported.GetStringSlice("users"))
		fp, err = app.FindFirstRecordByFilter("fingerprints", "system={:id}", map[string]any{"id": imported.Id})
		require.NoError(t, err)
		require.Equal(t, "original-agent-token", fp.GetString("token"))
		require.Empty(t, fp.GetString("fingerprint"))
		code, _ = request(adminToken, yaml, "current", true, plan["preview_id"].(string))
		require.Equal(t, 409, code, "stale confirmation is rejected")
		code, plan = request(adminToken, yaml, "current", false, "")
		require.Equal(t, 200, code)
		require.EqualValues(t, 0, plan["add"])
		require.EqualValues(t, 3, plan["skip"])
		code, applied = request(adminToken, yaml, "current", true, plan["preview_id"].(string))
		require.Equal(t, 200, code)
		require.EqualValues(t, 0, applied["created"])
	})
	t.Run("user mapping and duplicate input", func(t *testing.T) {
		yaml := "systems:\n- name: assigned\n  host: 127.0.0.4\n  port: 9\n  users: [import-owner@example.com]\n- name: duplicate\n  host: 127.0.0.4\n  port: 9\n"
		code, plan := request(adminToken, yaml, "match", false, "")
		require.Equal(t, 200, code)
		require.EqualValues(t, 1, plan["add"])
		require.EqualValues(t, 1, plan["skip"])
		code, _ = request(adminToken, yaml, "match", true, plan["preview_id"].(string))
		require.Equal(t, 200, code)
		record, err := app.FindFirstRecordByFilter("systems", "name='assigned'")
		require.NoError(t, err)
		require.Equal(t, []string{other.Id}, record.GetStringSlice("users"))
	})
	t.Run("invalid rows cannot partially apply", func(t *testing.T) {
		before := count()
		for _, tail := range []string{
			"- name: no host",
			"- name: short token\n  host: 127.0.0.6\n  token: short",
			"- name: conflict\n  host: 127.0.0.6\n  token: original-agent-token",
			"- name: unknown\n  host: 127.0.0.6\n  users: [not-found@example.com]",
		} {
			yaml := "systems:\n- name: valid first row\n  host: 127.0.0.5\n  port: 9\n" + tail
			code, plan := request(adminToken, yaml, "match", false, "")
			require.Equal(t, 200, code)
			require.Greater(t, plan["errors"], float64(0))
			code, _ = request(adminToken, yaml, "match", true, plan["preview_id"].(string))
			require.Equal(t, 409, code)
			require.Equal(t, before, count())
		}
	})
	t.Run("atomic rollback", func(t *testing.T) {
		before := count()
		yaml := "systems:\n- name: rollback first\n  host: 127.0.0.7\n  port: 9\n- name: rollback second\n  host: 127.0.0.8\n  port: 9\n  token: reject-this-token"
		code, plan := request(adminToken, yaml, "current", false, "")
		require.Equal(t, 200, code)
		hook := app.OnRecordCreate("fingerprints").BindFunc(func(e *core.RecordEvent) error {
			if e.Record.GetString("token") == "reject-this-token" {
				return errors.New("simulated fingerprint save failure")
			}
			return e.Next()
		})
		defer app.OnRecordCreate("fingerprints").Unbind(hook)
		code, _ = request(adminToken, yaml, "current", true, plan["preview_id"].(string))
		require.Equal(t, 500, code)
		require.Equal(t, before, count())
		fps, err := app.CountRecords("fingerprints")
		require.NoError(t, err)
		require.EqualValues(t, before, fps, "no orphan fingerprint")
	})
	t.Run("default port normalized addresses and socket duplicates", func(t *testing.T) {
		yaml := `systems:
- name: default port
  host: DEMO.EXAMPLE.
- name: duplicate domain
  host: demo.example
  port: 45876
- name: ipv6
  host: "::1"
  port: 9
- name: equivalent ipv6
  host: "0:0:0:0:0:0:0:1"
  port: 9
- name: socket
  host: /tmp/import-demo.sock
  port: 1
- name: same socket
  host: /tmp/import-demo.sock
  port: 2
`
		before := count()
		code, plan := request(adminToken, yaml, "current", false, "")
		require.Equal(t, 200, code)
		require.EqualValues(t, 3, plan["add"])
		require.EqualValues(t, 3, plan["skip"])
		require.EqualValues(t, 0, plan["errors"])
		require.Equal(t, before, count())
		rows := plan["rows"].([]any)
		require.EqualValues(t, 45876, rows[0].(map[string]any)["port"])
		code, _ = request(adminToken, strings.Replace(yaml, "default port", "changed after preview", 1), "current", true, plan["preview_id"].(string))
		require.Equal(t, 409, code)
		require.Equal(t, before, count())
	})
	t.Run("strict parsing bounded input and startup sync", func(t *testing.T) {
		for _, yaml := range []string{"", "systems: []", "systems: [", "systems:\n- name: bad port\n  host: 127.0.0.9\n  port: 65536", "systems:\n- name: typo\n  host: 127.0.0.9\n  tokne: secret-never-return", "systems: []\n---\nsystems: []", strings.Repeat("x", (1<<20)+1)} {
			code, result := request(adminToken, yaml, "current", false, "")
			require.Equal(t, 400, code)
			encoded, _ := json.Marshal(result)
			require.NotContains(t, string(encoded), "secret-never-return")
		}
		configPath := filepath.Join(app.DataDir(), "config.yml")
		require.NoError(t, os.WriteFile(configPath, []byte("systems: []"), 0600))
		defer os.Remove(configPath)
		code, result := request(adminToken, yaml, "current", false, "")
		require.Equal(t, 409, code)
		require.Equal(t, "managed_config", result["code"])
	})
}
