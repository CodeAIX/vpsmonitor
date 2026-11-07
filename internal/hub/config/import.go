package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
	"uuid"

	"github.com/CodeAIX/vpsmonitor/internal/entities/system"
	"github.com/CodeAIX/vpsmonitor/internal/entities/traffic"
	"github.com/pocketbase/pocketbase/core"
	"gopkg.in/yaml.v3"
)

const maxImportBytes = 1 << 20

// Serialize imports; other system edits are rechecked inside the transaction.
var importMu sync.Mutex

type importRequest struct {
	Config       string `json:"config"`
	UserMode     string `json:"user_mode"`
	Apply        bool   `json:"apply"`
	PreviewID    string `json:"preview_id"`
	ApplyTraffic bool   `json:"apply_traffic"`
}

type importRow struct {
	Name           string            `json:"name"`
	Host           string            `json:"host"`
	Port           uint16            `json:"port"`
	Action         string            `json:"action"`
	Reason         string            `json:"reason,omitempty"`
	Traffic        *traffic.Settings `json:"traffic,omitempty"`
	CurrentTraffic *traffic.Settings `json:"current_traffic,omitempty"`
}

type importPlan struct {
	Rows      []importRow `json:"rows"`
	Add       int         `json:"add"`
	Skip      int         `json:"skip"`
	Errors    int         `json:"errors"`
	Created   int         `json:"created"`
	Update    int         `json:"update"`
	Updated   int         `json:"updated"`
	PreviewID string      `json:"preview_id"`
}

// Tokens and resolved ownership are deliberately absent from the response.
type plannedSystem struct {
	Config systemConfig
	Users  []string
	Row    importRow
	Target string
}

// ImportYamlConfig adds systems without updating/deleting existing records or
// writing config.yml. The route must also bind the Hub's admin middleware.
func ImportYamlConfig(e *core.RequestEvent) error {
	if e.Auth == nil || e.Auth.Collection().Name != "users" || e.Auth.GetString("role") != "admin" {
		return e.ForbiddenError("Administrator access required.", nil)
	}
	e.Response.Header().Set("Cache-Control", "no-store")
	// Limit before decoding, including when Content-Length is absent or forged.
	e.Request.Body = http.MaxBytesReader(e.Response, e.Request.Body, 2*maxImportBytes)
	var req importRequest
	dec := json.NewDecoder(e.Request.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return e.BadRequestError("Invalid import request (maximum 1 MiB YAML).", nil)
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return e.BadRequestError("Only one import request is allowed.", nil)
	}
	if req.UserMode == "" {
		req.UserMode = "current"
	}
	if req.UserMode != "current" && req.UserMode != "match" {
		return e.BadRequestError("Invalid user mapping mode.", nil)
	}
	// Keep the one-time importer separate from file-managed configuration.
	if _, err := os.Stat(filepath.Join(e.App.DataDir(), "config.yml")); !os.IsNotExist(err) {
		return e.JSON(http.StatusConflict, map[string]string{"code": "managed_config"})
	}
	cfg, err := parseImport(req.Config)
	if err != nil {
		// YAML decoder errors can quote a token; never return/log their contents.
		return e.BadRequestError("Invalid YAML: expected 1–1000 systems with name, host, port, token and users fields (maximum 1 MiB).", nil)
	}
	importMu.Lock()
	defer importMu.Unlock()
	var plan importPlan
	var conflict bool
	err = e.App.RunInTransaction(func(tx core.App) error {
		var items []plannedSystem
		var err error
		plan, items, err = planImport(tx, cfg, req.UserMode, e.Auth.Id, req.ApplyTraffic)
		if err != nil {
			return err
		}
		if !req.Apply {
			return nil
		}
		if req.PreviewID == "" || req.PreviewID != plan.PreviewID || plan.Errors != 0 {
			conflict = true
			return nil
		}
		collection, err := tx.FindCollectionByNameOrId("systems")
		if err != nil {
			return err
		}
		for _, item := range items {
			if item.Row.Action == "update_traffic" {
				if err := savePortableTraffic(tx, item.Target, item.Config.Traffic); err != nil {
					return err
				}
				plan.Updated++
				continue
			}
			if item.Row.Action != "add" {
				continue
			}
			record := core.NewRecord(collection)
			record.Set("name", item.Config.Name)
			record.Set("host", item.Config.Host)
			record.Set("port", strconv.Itoa(int(item.Config.Port)))
			record.Set("users", item.Users)
			record.Set("info", system.Info{})
			record.Set("status", "pending")
			if err := tx.Save(record); err != nil {
				return err
			}
			token := item.Config.Token
			if token == "" {
				token = uuid.New().String()
			}
			if err := createFingerprintRecord(tx, record.Id, token); err != nil {
				return err
			}
			if err := savePortableTraffic(tx, record.Id, item.Config.Traffic); err != nil {
				return err
			}
			plan.Created++
		}
		return nil
	})
	if err != nil {
		// Save/validation errors may contain token values; the entire transaction
		// rolls back and only a generic error is returned.
		return e.InternalServerError("Import failed. No systems were imported.", nil)
	}
	if conflict {
		return e.JSON(http.StatusConflict, map[string]any{"code": "preview_changed", "plan": plan})
	}
	return e.JSON(http.StatusOK, plan)
}

func parseImport(content string) (config, error) {
	var cfg config
	if len(content) == 0 || len(content) > maxImportBytes {
		return cfg, errors.New("size")
	}
	decoder := yaml.NewDecoder(strings.NewReader(strings.TrimPrefix(content, "\ufeff")))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return cfg, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return cfg, errors.New("multiple documents")
	}
	if len(cfg.Systems) == 0 || len(cfg.Systems) > 1000 {
		return cfg, errors.New("system count")
	}
	if (cfg.Kind != "" && cfg.Kind != "vpsmonitor-config" && cfg.Kind != "vpsmonitor-traffic") || cfg.TrafficSchema > 1 {
		return cfg, errors.New("unsupported config schema")
	}
	if cfg.Kind != "" && cfg.TrafficSchema != 1 {
		return cfg, errors.New("missing config schema")
	}
	for _, entry := range cfg.Systems {
		if entry.ID != "" {
			return cfg, errors.New("same-Hub sync configuration")
		}
	}
	return cfg, nil
}

func importEndpoint(host, port string) string {
	if strings.HasPrefix(host, "/") {
		return host // Unix socket ports are ignored by the agent connection code.
	}
	if port == "" || port == "0" {
		port = "45876"
	}
	if n, err := strconv.Atoi(port); err == nil {
		port = strconv.Itoa(n)
	}
	if addr, err := netip.ParseAddr(strings.Trim(host, "[]")); err == nil {
		host = addr.Unmap().String()
	}
	return strings.ToLower(strings.TrimSuffix(host, ".")) + "\x00" + port
}

func invalidImportText(s string) bool {
	return !utf8.ValidString(s) || strings.IndexFunc(s, unicode.IsControl) >= 0
}

func planImport(app core.App, cfg config, mode, owner string, applyTraffic bool) (importPlan, []plannedSystem, error) {
	plan := importPlan{Rows: make([]importRow, 0, len(cfg.Systems))}
	users, err := app.FindAllRecords("users")
	if err != nil {
		return plan, nil, err
	}
	emails := map[string]string{}
	for _, u := range users {
		emails[strings.ToLower(u.GetString("email"))] = u.Id
	}
	systems, err := app.FindAllRecords("systems")
	if err != nil {
		return plan, nil, err
	}
	endpoints := map[string]string{}
	ambiguous := map[string]bool{}
	for _, s := range systems {
		key := importEndpoint(s.GetString("host"), s.GetString("port"))
		if endpoints[key] != "" {
			ambiguous[key] = true
		}
		endpoints[key] = s.Id
	}
	fingerprints, err := app.FindAllRecords("fingerprints")
	if err != nil {
		return plan, nil, err
	}
	tokens := map[string]string{}
	for _, fp := range fingerprints {
		tokens[fp.GetString("token")] = fp.GetString("system")
	}
	items := make([]plannedSystem, 0, len(cfg.Systems))
	seen := map[string]bool{}
	for i, s := range cfg.Systems {
		s.Name, s.Host = strings.TrimSpace(s.Name), strings.TrimSpace(s.Host)
		if s.Port == 0 {
			s.Port = 45876
		}
		item := plannedSystem{Config: s, Users: []string{owner}, Row: importRow{Name: s.Name, Host: s.Host, Port: s.Port, Action: "add"}}
		item.Row.Traffic = s.Traffic
		endpoint := importEndpoint(s.Host, strconv.Itoa(int(s.Port)))
		if s.Name == "" || len(s.Name) > 255 || s.Host == "" || len(s.Host) > 1024 || invalidImportText(s.Name) || invalidImportText(s.Host) || strings.ContainsAny(s.Host, " \t") {
			item.Row.Action, item.Row.Reason = "error", "invalid_system"
		} else if s.Token != "" && (utf8.RuneCountInString(s.Token) < 9 || utf8.RuneCountInString(s.Token) > 255 || invalidImportText(s.Token) || strings.TrimSpace(s.Token) != s.Token) {
			item.Row.Action, item.Row.Reason = "error", "invalid_token"
		} else if s.Traffic != nil && s.Traffic.Validate() != nil {
			item.Row.Action, item.Row.Reason = "error", "invalid_traffic"
		} else if s.Traffic != nil && seen[endpoint] {
			item.Row.Action, item.Row.Reason = "error", "duplicate_traffic"
		} else if id := endpoints[endpoint]; id != "" {
			item.Row.Action, item.Row.Reason, item.Target = "skip", "existing_endpoint", id
			if s.Traffic != nil && applyTraffic {
				if ambiguous[endpoint] {
					item.Row.Action, item.Row.Reason = "error", "ambiguous_endpoint"
				} else {
					previous, err := readPortableTraffic(app, id)
					if err != nil {
						return plan, nil, err
					}
					item.Row.CurrentTraffic = previous
					if previous == nil || *previous != *s.Traffic {
						item.Row.Action, item.Row.Reason = "update_traffic", "update_traffic"
					} else {
						item.Row.Reason = "same_traffic"
					}
				}
			} else if s.Traffic != nil {
				item.Row.Reason = "traffic_not_applied"
			}
		} else if cfg.Kind == "vpsmonitor-traffic" {
			item.Row.Action, item.Row.Reason = "error", "unknown_endpoint"
		} else if s.Token != "" && tokens[s.Token] != "" {
			item.Row.Action, item.Row.Reason = "error", "token_conflict"
		} else if mode == "match" && len(s.Users) > 0 {
			item.Users = nil
			seen := map[string]bool{}
			for _, email := range s.Users {
				id := emails[strings.ToLower(strings.TrimSpace(email))]
				if id == "" {
					item.Row.Action, item.Row.Reason = "error", "unknown_user"
					break
				}
				if !seen[id] {
					item.Users = append(item.Users, id)
					seen[id] = true
				}
			}
		}
		if cfg.Kind == "vpsmonitor-traffic" && s.Traffic == nil {
			item.Row.Action, item.Row.Reason = "error", "invalid_traffic"
		}
		seen[endpoint] = true
		switch item.Row.Action {
		case "add":
			plan.Add++
			// Reserve both values for later rows in the same file.
			endpoints[endpoint] = "import-row-" + strconv.Itoa(i)
			if s.Token != "" {
				tokens[s.Token] = endpoints[endpoint]
			} else {
				item.Row.Reason = "generated_token"
			}
		case "skip":
			plan.Skip++
		case "update_traffic":
			plan.Update++
		case "error":
			plan.Errors++
		}
		items = append(items, item)
		plan.Rows = append(plan.Rows, item.Row)
	}
	// Bind confirmation to this input, user mapping and current duplicate state.
	encoded, _ := json.Marshal(struct {
		Owner string
		Mode  string
		Items []plannedSystem
	}{owner, mode, items})
	digest := sha256.Sum256(encoded)
	plan.PreviewID = hex.EncodeToString(digest[:])
	return plan, items, nil
}
