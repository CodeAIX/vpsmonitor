//go:build testing

package hub_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/CodeAIX/vpsmonitor/internal/hub/config"
	hubTests "github.com/CodeAIX/vpsmonitor/internal/tests"
	"github.com/pocketbase/pocketbase/core"
	"github.com/stretchr/testify/require"
)

func TestStartupSyncPreviewAndRename(t *testing.T) {
	app, handler := firstUserTestMux(t)
	t.Cleanup(app.Cleanup)
	admin, err := hubTests.CreateUserWithRole(app, "sync-admin@example.com", "password123", "admin")
	require.NoError(t, err)
	owner, err := hubTests.CreateUserWithRole(app, "sync-owner@example.com", "password123", "user")
	require.NoError(t, err)
	token, err := admin.NewAuthToken()
	require.NoError(t, err)
	ownerToken, err := owner.NewAuthToken()
	require.NoError(t, err)
	record, err := hubTests.CreateRecord(app, "systems", map[string]any{
		"name": "before", "host": "old.example", "port": "43210", "users": []string{owner.Id},
	})
	require.NoError(t, err)
	fp, err := hubTests.CreateRecord(app, "fingerprints", map[string]any{
		"system": record.Id, "token": "keep-original-token", "fingerprint": "keep-original-fingerprint",
	})
	require.NoError(t, err)
	_, err = hubTests.CreateRecord(app, "systems", map[string]any{"name": "unlisted", "host": "unlisted.example", "users": []string{admin.Id}})
	require.NoError(t, err)
	yaml := fmt.Sprintf("systems:\n- id: %s\n  name: after\n  host: new.example\n  token: yaml-token-never-applied\n", record.Id)
	preview := func(auth, input string) (int, map[string]any) {
		body, err := json.Marshal(map[string]string{"config": input})
		require.NoError(t, err)
		return trafficAPIRequest(t, handler, "POST", "/api/vpsmonitor/config-yaml/sync-preview", auth, string(body))
	}
	load := func() *core.Record {
		record, err := app.FindRecordById("systems", record.Id)
		require.NoError(t, err)
		return record
	}
	code, _ := preview(ownerToken, yaml)
	require.Equal(t, 403, code)
	code, plan := preview(token, yaml)
	require.Equal(t, 200, code)
	require.EqualValues(t, 1, plan["retained"])
	require.Equal(t, []any{map[string]any{
		"id": record.Id, "name": "after", "action": "update", "ownership_changed": false,
	}}, plan["rows"])
	require.Equal(t, "before", load().GetString("name"))
	code, _ = preview(token, yaml+"  users: [missing@example.com]\n")
	require.Equal(t, 400, code)
	code, _ = preview(token, yaml+"  users: []\n")
	require.Equal(t, 400, code)

	path := filepath.Join(app.DataDir(), "config.yml")
	event := &core.ServeEvent{App: app}
	require.NoError(t, os.WriteFile(path, []byte(yaml+"- id: nonexistent\n  name: unknown\n  host: unknown.example\n"), 0600))
	require.ErrorContains(t, config.SyncSystems(event), "ID not found")
	require.Equal(t, "before", load().GetString("name"))
	require.NoError(t, os.WriteFile(path, []byte(yaml), 0600))
	require.NoError(t, config.SyncSystems(event))
	updated := load()
	require.Equal(t, "after", updated.GetString("name"))
	require.Equal(t, "new.example", updated.GetString("host"))
	require.Equal(t, "43210", updated.GetString("port"))
	require.Equal(t, []string{owner.Id}, updated.GetStringSlice("users"))
	savedFP, err := app.FindRecordById("fingerprints", fp.Id)
	require.NoError(t, err)
	require.Equal(t, "keep-original-token", savedFP.GetString("token"))
	require.Equal(t, "keep-original-fingerprint", savedFP.GetString("fingerprint"))
	count, err := app.CountRecords("systems")
	require.NoError(t, err)
	require.EqualValues(t, 2, count)
}
