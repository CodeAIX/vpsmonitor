// Package config provides functions for syncing systems with the config.yml file
package config

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"uuid"

	"github.com/CodeAIX/vpsmonitor/internal/entities/system"
	"github.com/CodeAIX/vpsmonitor/internal/entities/traffic"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/spf13/cast"
	"gopkg.in/yaml.v3"
)

type config struct {
	Kind          string         `yaml:"kind,omitempty"`
	TrafficSchema uint8          `yaml:"traffic_schema,omitempty"`
	Systems       []systemConfig `yaml:"systems"`
}

type systemConfig struct {
	ID      string            `yaml:"id,omitempty"`
	Name    string            `yaml:"name"`
	Host    string            `yaml:"host"`
	Port    uint16            `yaml:"port,omitempty"`
	Token   string            `yaml:"token,omitempty"`
	Users   []string          `yaml:"users,omitempty"`
	Traffic *traffic.Settings `yaml:"traffic,omitempty"`
}

// Syncs systems with the config.yml file
func SyncSystems(e *core.ServeEvent) error {
	h := e.App
	configPath := filepath.Join(h.DataDir(), "config.yml")
	configData, err := os.ReadFile(configPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("could not read config.yml: %w", err)
	}
	configured, err := parseSyncConfig(configData)
	if err != nil {
		return err
	}
	return h.RunInTransaction(func(tx core.App) error {
		plan, err := planSync(tx, configured)
		if err != nil {
			return err
		}
		if err := applySync(tx, plan); err != nil {
			return err
		}
		log.Println("Systems synced with config.yml. Existing identities and unlisted systems retained.")
		return nil
	})
}

func parseSyncConfig(data []byte) ([]systemConfig, error) {
	var cfg config
	if len(data) > maxImportBytes || yaml.Unmarshal(data, &cfg) != nil {
		return nil, fmt.Errorf("invalid config.yml (maximum 1 MiB)")
	}
	return cfg.Systems, nil
}

type syncRow struct {
	ID               string `json:"id,omitempty"`
	Name             string `json:"name"`
	Action           string `json:"action"`
	OwnershipChanged bool   `json:"ownership_changed"`
}

type syncItem struct {
	row    syncRow
	config systemConfig
	target *core.Record
	users  []string
}

type syncPlan struct {
	items    []syncItem
	retained int
}

// PreviewSyncConfig uses the same planner as startup but never applies the plan.
func PreviewSyncConfig(e *core.RequestEvent) error {
	e.Response.Header().Set("Cache-Control", "no-store")
	e.Request.Body = http.MaxBytesReader(e.Response, e.Request.Body, 2*maxImportBytes)
	var req struct {
		Config string `json:"config"`
	}
	if err := e.BindBody(&req); err != nil || req.Config == "" {
		return e.BadRequestError("Expected config.yml content.", nil)
	}
	configured, err := parseSyncConfig([]byte(req.Config))
	if err != nil {
		return e.BadRequestError(err.Error(), nil)
	}
	plan, err := planSync(e.App, configured)
	if err != nil {
		return e.BadRequestError(err.Error(), nil)
	}
	rows := make([]syncRow, 0, len(plan.items))
	for _, item := range plan.items {
		rows = append(rows, item.row)
	}
	return e.JSON(200, map[string]any{"rows": rows, "retained": plan.retained})
}

func planSync(h core.App, configured []systemConfig) (syncPlan, error) {
	var plan syncPlan
	users, err := h.FindRecordsByFilter("users", "id != ''", "created,id", -1, 0)
	if err != nil {
		return plan, fmt.Errorf("could not read users")
	}
	userIDs := make(map[string]string, len(users))
	for _, user := range users {
		userIDs[user.GetString("email")] = user.Id
	}

	existing, err := h.FindAllRecords("systems", dbx.NewExp("id != ''"))
	if err != nil {
		return plan, fmt.Errorf("could not read systems")
	}
	type systemKey struct{ name, host, port string }
	byID := make(map[string]*core.Record, len(existing))
	byKey := make(map[systemKey][]*core.Record, len(existing))
	for _, record := range existing {
		byID[record.Id] = record
		key := systemKey{record.GetString("name"), record.GetString("host"), record.GetString("port")}
		byKey[key] = append(byKey[key], record)
	}
	seenIDs := make(map[string]bool)
	seenKeys := make(map[systemKey]bool)
	for i, entry := range configured {
		if entry.Name == "" || entry.Host == "" {
			return plan, fmt.Errorf("config.yml system %d requires name and host", i+1)
		}
		var target *core.Record
		if entry.ID != "" {
			target = byID[entry.ID]
			if target == nil {
				return plan, fmt.Errorf("config.yml system %d has an ID not found on this Hub", i+1)
			}
			if entry.Port == 0 {
				entry.Port = cast.ToUint16(target.Get("port"))
			}
		}
		if entry.Port == 0 {
			entry.Port = 45876
		}
		key := systemKey{entry.Name, entry.Host, cast.ToString(entry.Port)}
		matches := byKey[key]
		if seenKeys[key] || len(matches) > 1 || (len(matches) == 1 && target != nil && matches[0] != target) {
			return plan, fmt.Errorf("config.yml system %d has a conflicting name, host and port", i+1)
		}
		seenKeys[key] = true
		if target == nil && len(matches) == 1 {
			target = matches[0]
		}
		item := syncItem{row: syncRow{Name: entry.Name, Action: "add"}, config: entry, target: target}
		if entry.Users == nil {
			if target != nil {
				item.users = target.GetStringSlice("users")
			} else if len(users) > 0 {
				item.users = []string{users[0].Id}
			}
		} else {
			if len(entry.Users) == 0 {
				return plan, fmt.Errorf("config.yml system %d needs at least one user; omit users to keep ownership", i+1)
			}
			item.users = make([]string, 0, len(entry.Users))
			for _, email := range entry.Users {
				id, ok := userIDs[email]
				if !ok {
					return plan, fmt.Errorf("config.yml system %d lists an unknown user", i+1)
				}
				if !slices.Contains(item.users, id) {
					item.users = append(item.users, id)
				}
			}
		}
		if target != nil {
			if seenIDs[target.Id] {
				return plan, fmt.Errorf("config.yml system %d targets an already listed device", i+1)
			}
			seenIDs[target.Id] = true
			item.row.ID = target.Id
			before := target.GetStringSlice("users")
			item.row.OwnershipChanged = len(before) != len(item.users) || slices.ContainsFunc(before, func(id string) bool {
				return !slices.Contains(item.users, id)
			})
			item.row.Action = "keep"
			if item.row.OwnershipChanged || entry.Name != target.GetString("name") ||
				entry.Host != target.GetString("host") || key.port != target.GetString("port") {
				item.row.Action = "update"
			}
		}
		plan.items = append(plan.items, item)
	}
	plan.retained = len(existing) - len(seenIDs)
	return plan, nil
}

func applySync(h core.App, plan syncPlan) error {
	collection, err := h.FindCollectionByNameOrId("systems")
	if err != nil {
		return err
	}
	for i, item := range plan.items {
		record := item.target
		if item.row.Action == "keep" {
			continue
		}
		if record == nil {
			record = core.NewRecord(collection)
			record.Set("info", system.Info{})
			record.Set("status", "pending")
		}
		record.Set("name", item.config.Name)
		record.Set("host", item.config.Host)
		record.Set("port", item.config.Port)
		record.Set("users", item.users)
		if err := h.Save(record); err != nil {
			return fmt.Errorf("could not save config.yml system %d", i+1)
		}
		if item.target == nil {
			token := item.config.Token
			if token == "" {
				token = uuid.New().String()
			}
			if err := createFingerprintRecord(h, record.Id, token); err != nil {
				return fmt.Errorf("could not create identity for config.yml system %d", i+1)
			}
		}
	}
	return nil
}

// Generates content for the config.yml file as a YAML string
func generateYAML(h core.App) (string, error) {
	return generateYAMLFormat(h, "classic")
}

func generateYAMLFormat(h core.App, format string) (string, error) {
	// Fetch all systems from the database
	systems, err := h.FindRecordsByFilter("systems", "id != ''", "name", -1, 0)
	if err != nil {
		return "", err
	}

	// Create a Config struct to hold the data
	config := config{
		Systems: make([]systemConfig, 0, len(systems)),
	}
	if format != "classic" && format != "sync" {
		config.Kind, config.TrafficSchema = "vpsmonitor-config", 1
		if format == "traffic" {
			config.Kind = "vpsmonitor-traffic"
		}
	}

	// Fetch all users at once
	allUserIDs := make([]string, 0)
	for _, system := range systems {
		allUserIDs = append(allUserIDs, system.GetStringSlice("users")...)
	}
	userEmailMap, err := getUserEmailMap(h, allUserIDs)
	if err != nil {
		return "", err
	}

	// Fetch all fingerprint records to get tokens
	type fingerprintData struct {
		ID     string `db:"id"`
		System string `db:"system"`
		Token  string `db:"token"`
	}
	var fingerprints []fingerprintData
	err = h.DB().NewQuery("SELECT id, system, token FROM fingerprints").All(&fingerprints)
	if err != nil {
		return "", err
	}

	// Create a map of system ID to token
	systemTokenMap := make(map[string]string)
	for _, fingerprint := range fingerprints {
		systemTokenMap[fingerprint.System] = fingerprint.Token
	}

	// Populate the Config struct with system data
	for _, system := range systems {
		userIDs := system.GetStringSlice("users")
		userEmails := make([]string, 0, len(userIDs))
		for _, userID := range userIDs {
			if email, ok := userEmailMap[userID]; ok {
				userEmails = append(userEmails, email)
			}
		}

		sysConfig := systemConfig{
			Name:  system.GetString("name"),
			Host:  system.GetString("host"),
			Port:  cast.ToUint16(system.Get("port")),
			Users: userEmails,
			Token: systemTokenMap[system.Id],
		}
		if format != "classic" && format != "sync" {
			sysConfig.Traffic, err = readPortableTraffic(h, system.Id)
			if err != nil {
				return "", err
			}
		}
		if format == "sync" {
			sysConfig.ID, sysConfig.Token, sysConfig.Users = system.Id, "", nil
		}
		if format == "traffic" {
			if sysConfig.Traffic == nil {
				continue
			}
			sysConfig.Token, sysConfig.Users = "", nil
		}
		config.Systems = append(config.Systems, sysConfig)
	}

	// Marshal the Config struct to YAML
	yamlData, err := yaml.Marshal(&config)
	if err != nil {
		return "", err
	}

	// Add a header to the YAML
	header := "# Values for port, users, and token are optional.\n# Defaults are port 45876, the first created user, and a generated UUID token.\n\n"
	if format != "classic" && format != "sync" {
		header = "# vpsmonitor portable configuration. Import through Settings > YAML Config.\n# No monitoring history or Hub private key is included.\n\n"
	}
	if format == "sync" {
		header = "# Startup sync for this Hub only. IDs refer to existing devices.\n# Omitted users preserve ownership; explicit users replace it.\n\n"
	}
	yamlData = append([]byte(header), yamlData...)

	return string(yamlData), nil
}

// New helper function to get a map of user IDs to emails
func getUserEmailMap(h core.App, userIDs []string) (map[string]string, error) {
	users, err := h.FindRecordsByIds("users", userIDs)
	if err != nil {
		return nil, err
	}

	userEmailMap := make(map[string]string, len(users))
	for _, user := range users {
		userEmailMap[user.Id] = user.GetString("email")
	}

	return userEmailMap, nil
}

// Helper function to create a new fingerprint record for a system
func createFingerprintRecord(app core.App, systemID, token string) error {
	fingerprintsCollection, err := app.FindCollectionByNameOrId("fingerprints")
	if err != nil {
		return fmt.Errorf("failed to find fingerprints collection: %v", err)
	}

	newFingerprint := core.NewRecord(fingerprintsCollection)
	newFingerprint.Set("system", systemID)
	newFingerprint.Set("token", token)
	newFingerprint.Set("fingerprint", "") // Empty fingerprint, will be set on first connection

	return app.Save(newFingerprint)
}

// Returns the current config.yml file as a JSON object
func GetYamlConfig(e *core.RequestEvent) error {
	e.Response.Header().Set("Cache-Control", "no-store")
	format := e.Request.URL.Query().Get("format")
	if format == "" {
		format = "classic"
	}
	if format != "classic" && format != "combined" && format != "traffic" && format != "sync" {
		return e.BadRequestError("Invalid export format", nil)
	}
	configContent, err := generateYAMLFormat(e.App, format)
	if err != nil {
		return err
	}
	return e.JSON(200, map[string]string{"config": configContent})
}
