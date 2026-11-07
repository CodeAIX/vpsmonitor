// Package config provides functions for syncing systems with the config.yml file
package config

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
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
	if err != nil {
		return nil
	}

	var config config
	err = yaml.Unmarshal(configData, &config)
	if err != nil {
		return fmt.Errorf("failed to parse config.yml")
	}

	if len(config.Systems) == 0 {
		log.Println("No systems defined in config.yml.")
		return nil
	}
	return h.RunInTransaction(func(tx core.App) error {
		return syncSystems(tx, config.Systems)
	})
}

func syncSystems(h core.App, configured []systemConfig) error {
	var firstUser *core.Record

	// Create a map of email to user ID
	userEmailToID := make(map[string]string)
	users, err := h.FindAllRecords("users", dbx.NewExp("id != ''"))
	if err != nil {
		return err
	}
	if len(users) > 0 {
		firstUser = users[0]
		for _, user := range users {
			userEmailToID[user.GetString("email")] = user.Id
		}
	}

	// add default settings for systems if not defined in config
	for i := range configured {
		system := &configured[i]
		if system.Port == 0 {
			system.Port = 45876
		}
		if len(users) > 0 && len(system.Users) == 0 {
			// default to first user if none are defined
			system.Users = []string{firstUser.Id}
		} else {
			// Convert email addresses to user IDs
			userIDs := make([]string, 0, len(system.Users))
			for _, email := range system.Users {
				if id, ok := userEmailToID[email]; ok {
					userIDs = append(userIDs, id)
				} else {
					log.Printf("User %s not found", email)
				}
			}
			system.Users = userIDs
		}
	}

	// Get existing systems
	existingSystems, err := h.FindAllRecords("systems", dbx.NewExp("id != ''"))
	if err != nil {
		return err
	}

	// Create a map of existing systems
	type systemKey struct{ name, host, port string }
	existingSystemsMap := make(map[systemKey]*core.Record)
	for _, system := range existingSystems {
		key := systemKey{system.GetString("name"), system.GetString("host"), system.GetString("port")}
		existingSystemsMap[key] = system
	}

	// Process systems from config
	for _, sysConfig := range configured {
		key := systemKey{sysConfig.Name, sysConfig.Host, cast.ToString(sysConfig.Port)}
		if existingSystem, ok := existingSystemsMap[key]; ok {
			// Update existing system
			existingSystem.Set("name", sysConfig.Name)
			existingSystem.Set("users", sysConfig.Users)
			existingSystem.Set("port", sysConfig.Port)
			if err := h.Save(existingSystem); err != nil {
				return err
			}

		} else {
			// Create new system
			systemsCollection, err := h.FindCollectionByNameOrId("systems")
			if err != nil {
				return fmt.Errorf("failed to find systems collection: %v", err)
			}
			newSystem := core.NewRecord(systemsCollection)
			newSystem.Set("name", sysConfig.Name)
			newSystem.Set("host", sysConfig.Host)
			newSystem.Set("port", sysConfig.Port)
			newSystem.Set("users", sysConfig.Users)
			newSystem.Set("info", system.Info{})
			newSystem.Set("status", "pending")
			if err := h.Save(newSystem); err != nil {
				return fmt.Errorf("failed to create new system: %v", err)
			}

			// For new systems, generate token if not provided
			token := sysConfig.Token
			if token == "" {
				token = uuid.New().String()
			}

			// Create fingerprint record for new system
			if err := createFingerprintRecord(h, newSystem.Id, token); err != nil {
				return err
			}
			existingSystemsMap[key] = newSystem
		}
	}

	log.Println("Systems synced with config.yml. Existing identities and unlisted systems retained.")
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
	if format != "classic" {
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
		if format != "classic" {
			sysConfig.Traffic, err = readPortableTraffic(h, system.Id)
			if err != nil {
				return "", err
			}
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
	if format != "classic" {
		header = "# vpsmonitor portable configuration. Import through Settings > YAML Config.\n# No monitoring history or Hub private key is included.\n\n"
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
	if format != "classic" && format != "combined" && format != "traffic" {
		return e.BadRequestError("Invalid export format", nil)
	}
	configContent, err := generateYAMLFormat(e.App, format)
	if err != nil {
		return err
	}
	return e.JSON(200, map[string]string{"config": configContent})
}
