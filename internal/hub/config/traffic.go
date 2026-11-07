package config

import (
	"database/sql"
	"errors"

	"github.com/CodeAIX/vpsmonitor/internal/entities/traffic"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

func readPortableTraffic(app core.App, id string) (*traffic.Settings, error) {
	r, err := app.FindFirstRecordByFilter("traffic_settings", "system={:id}", dbx.Params{"id": id})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	s := &traffic.Settings{Enabled: r.GetBool("enabled"), Iface: r.GetString("iface"), QuotaBytes: uint64(r.GetInt64("quota_bytes")), CountMode: r.GetString("count_mode"), BillingDay: r.GetInt("billing_day"), BillingTimezone: r.GetString("billing_timezone"), DisplayUnit: r.GetString("display_unit")}
	if err := s.Validate(); err != nil {
		return nil, err
	}
	return s, nil
}

func savePortableTraffic(app core.App, id string, s *traffic.Settings) error {
	if s == nil {
		return nil
	}
	if err := s.Validate(); err != nil {
		return err
	}
	r, err := app.FindFirstRecordByFilter("traffic_settings", "system={:id}", dbx.Params{"id": id})
	if errors.Is(err, sql.ErrNoRows) {
		c, err := app.FindCollectionByNameOrId("traffic_settings")
		if err != nil {
			return err
		}
		r = core.NewRecord(c)
	} else if err != nil {
		return err
	}
	// Preserve all collector/source metadata. An interface may be restored before
	// its first collection; the existing view will show it as unavailable until then.
	r.Set("system", id)
	r.Set("enabled", s.Enabled)
	r.Set("iface", s.Iface)
	r.Set("quota_bytes", s.QuotaBytes)
	r.Set("count_mode", s.CountMode)
	r.Set("billing_day", s.BillingDay)
	r.Set("billing_timezone", s.BillingTimezone)
	r.Set("display_unit", s.DisplayUnit)
	return app.Save(r)
}
