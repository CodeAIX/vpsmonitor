package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(func(app core.App) error {
		systems, err := app.FindCollectionByNameOrId("systems")
		if err != nil {
			return err
		}
		read := "@request.auth.id != '' && system.users.id ?= @request.auth.id"
		zero := float64(0)
		for _, name := range []string{"agent_history_state", "agent_history_records"} {
			c := core.NewBaseCollection(name)
			c.ListRule = &read
			c.ViewRule = &read
			c.Fields.Add(&core.RelationField{Name: "system", CollectionId: systems.Id, Required: true, MaxSelect: 1, CascadeDelete: true})
			number := func(names ...string) {
				for _, key := range names {
					c.Fields.Add(&core.NumberField{Name: key, OnlyInt: true, Min: &zero})
				}
			}
			if name == "agent_history_state" {
				number("cursor", "oldest", "newest", "cache_samples", "cache_bytes", "last_sync", "retention_seconds", "gap_from", "gap_to")
				c.Fields.Add(&core.TextField{Name: "error", Max: 64}, &core.BoolField{Name: "limited"}, &core.BoolField{Name: "supported"})
				c.AddIndex("idx_agent_history_state", true, "system", "")
			} else {
				number("stamp", "first", "last", "samples", "bytes")
				c.Fields.Add(&core.SelectField{Name: "type", Required: true, MaxSelect: 1, Values: []string{"1m", "10m", "20m", "120m", "480m"}}, &core.JSONField{Name: "stats", MaxSize: 256 * 1024}, &core.JSONField{Name: "containers", MaxSize: 256 * 1024})
				c.AddIndex("idx_agent_history_records", true, "system,type,stamp", "")
			}
			if err := app.Save(c); err != nil {
				return err
			}
		}
		return nil
	}, func(app core.App) error {
		for _, name := range []string{"agent_history_records", "agent_history_state"} {
			c, err := app.FindCollectionByNameOrId(name)
			if err != nil {
				return err
			}
			if err = app.Delete(c); err != nil {
				return err
			}
		}
		return nil
	})
}
