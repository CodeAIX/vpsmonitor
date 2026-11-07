package migrations

import (
	"github.com/CodeAIX/vpsmonitor/internal/entities/traffic"
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(func(app core.App) error {
		systems, err := app.FindCollectionByNameOrId("systems")
		if err != nil {
			return err
		}
		readRule := "@request.auth.id != '' && system.users.id ?= @request.auth.id"
		zero, maxBytes := float64(0), float64(traffic.MaxSafeBytes)
		for _, name := range []string{"traffic_sources", "traffic_daily", "traffic_settings"} {
			c := core.NewBaseCollection(name)
			c.ListRule, c.ViewRule = &readRule, &readRule
			c.Fields.Add(&core.RelationField{Name: "system", CollectionId: systems.Id, Required: true, MaxSelect: 1, CascadeDelete: true})
			number := func(names ...string) {
				for _, key := range names {
					c.Fields.Add(&core.NumberField{Name: key, OnlyInt: true, Min: &zero, Max: &maxBytes})
				}
			}
			text := func(names ...string) {
				for _, key := range names {
					c.Fields.Add(&core.TextField{Name: key, Max: 128})
				}
			}
			switch name {
			case "traffic_daily":
				c.Fields.Add(&core.TextField{Name: "iface", Required: true, Max: 64}, &core.TextField{Name: "day", Required: true, Min: 10, Max: 10})
				number("rx_bytes", "tx_bytes", "observed_at")
				text("quality")
				c.AddIndex("idx_traffic_daily_identity", true, "system, iface, day", "")
				c.AddIndex("idx_traffic_daily_range", false, "system, iface, day DESC", "")
			case "traffic_sources":
				c.Fields.Add(&core.TextField{Name: "iface", Required: true, Max: 64})
				text("collector", "timebase", "status", "error_code", "first_available_day", "last_available_day", "quality", "utc_valid_from_day")
				number("schema_version", "first_seen_at", "last_observed_at", "last_success_at", "source_created_at", "source_updated_at", "refresh_seconds", "save_interval_seconds")
				c.AddIndex("idx_traffic_source_identity", true, "system, iface", "")
			case "traffic_settings":
				text("iface", "count_mode", "billing_timezone", "display_unit", "collector_status", "collector_error_code", "payload_digest")
				number("quota_bytes", "billing_day", "collector_last_observed_at", "collector_last_success_at")
				c.Fields.Add(&core.BoolField{Name: "enabled"})
				c.AddIndex("idx_traffic_settings_identity", true, "system", "")
			}
			if err := app.Save(c); err != nil {
				return err
			}
		}
		return nil
	}, func(app core.App) error {
		for _, name := range []string{"traffic_daily", "traffic_sources", "traffic_settings"} {
			c, err := app.FindCollectionByNameOrId(name)
			if err != nil {
				return err
			}
			if err := app.Delete(c); err != nil {
				return err
			}
		}
		return nil
	})
}
