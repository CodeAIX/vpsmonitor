package hub

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/CodeAIX/vpsmonitor/internal/entities/traffic"
	"github.com/CodeAIX/vpsmonitor/internal/hub/utils"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

type trafficSource struct {
	Iface               string `json:"iface"`
	CreatedAt           int64  `json:"source_created_at"`
	UpdatedAt           int64  `json:"source_updated_at"`
	LastSuccessAt       int64  `json:"last_success_at"`
	RefreshSeconds      int64  `json:"refresh_seconds"`
	SaveIntervalSeconds int64  `json:"save_interval_seconds"`
	UTCValidFrom        string `json:"utc_valid_from_day"`
	Quality             string `json:"quality"`
	ErrorCode           string `json:"error_code"`
}

type trafficView struct {
	System        string          `json:"system"`
	Iface         string          `json:"iface"`
	Status        string          `json:"status"`
	Freshness     string          `json:"freshness"`
	Quality       string          `json:"quality"`
	ErrorCode     string          `json:"error_code"`
	SystemStatus  string          `json:"system_status"`
	Settings      trafficSettings `json:"settings"`
	Sources       []trafficSource `json:"sources"`
	Source        *trafficSource  `json:"source"`
	Today         trafficSummary  `json:"today"`
	CalendarMonth trafficSummary  `json:"calendar_month"`
	BillingCycle  trafficSummary  `json:"billing_cycle"`
	Days          []trafficDay    `json:"days"`
	LastSuccessAt *string         `json:"last_success_at"`
	// Overview-compatible billing fields are also available at the top level.
	trafficSummary
	QuotaBytes uint64 `json:"quota_bytes"`
	CountMode  string `json:"count_mode"`
}

func defaultTrafficSettings(id string) trafficSettings {
	return trafficSettings{System: id, Enabled: true, CountMode: "tx", BillingDay: 1, BillingTimezone: "UTC", DisplayUnit: "GB"}
}

func settingsFromRecord(id string, record *core.Record) trafficSettings {
	if record == nil {
		return defaultTrafficSettings(id)
	}
	return trafficSettings{System: id, Enabled: record.GetBool("enabled"), Iface: record.GetString("iface"), QuotaBytes: uint64(record.GetInt64("quota_bytes")), CountMode: record.GetString("count_mode"), BillingDay: record.GetInt("billing_day"), BillingTimezone: record.GetString("billing_timezone"), DisplayUnit: record.GetString("display_unit")}
}

func canAccessTrafficSystem(user, system *core.Record) bool {
	if user == nil || system == nil {
		return false
	}
	share, _ := utils.GetEnv("SHARE_ALL_SYSTEMS")
	return share == "true" || slices.Contains(system.GetStringSlice("users"), user.Id)
}

func authorizedTrafficSystem(e *core.RequestEvent) (*core.Record, error) {
	record, err := e.App.FindRecordById("systems", e.Request.URL.Query().Get("system"))
	if err != nil || !canAccessTrafficSystem(e.Auth, record) {
		return nil, e.NotFoundError("System not found", nil)
	}
	return record, nil
}

func firstTrafficRecord(app core.App, name, id string) (*core.Record, error) {
	r, err := app.FindFirstRecordByFilter(name, "system={:system}", dbx.Params{"system": id})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return r, err
}

func buildTrafficView(ctx context.Context, app core.App, system *core.Record, now, from, to time.Time) (trafficView, error) {
	id := system.Id
	settingsRecord, err := firstTrafficRecord(app, "traffic_settings", id)
	if err != nil {
		return trafficView{}, err
	}
	settings := settingsFromRecord(id, settingsRecord)
	v := trafficView{System: id, Iface: settings.Iface, Settings: settings, QuotaBytes: settings.QuotaBytes, CountMode: settings.CountMode, SystemStatus: system.GetString("status"), Status: "not_enabled", Freshness: "unavailable", Quality: "unknown", Sources: []trafficSource{}, Days: []trafficDay{}}
	if settingsRecord != nil {
		if status := settingsRecord.GetString("collector_status"); status != "" {
			v.Status = status
		}
		v.ErrorCode = settingsRecord.GetString("collector_error_code")
	}
	var sources []*core.Record
	if err := app.RecordQuery("traffic_sources").WithContext(ctx).AndWhere(dbx.HashExp{"system": id}).OrderBy("iface").Limit(64).All(&sources); err != nil {
		return v, err
	}
	for _, record := range sources {
		source := trafficSource{Iface: record.GetString("iface"), CreatedAt: record.GetInt64("source_created_at"), UpdatedAt: record.GetInt64("source_updated_at"), LastSuccessAt: record.GetInt64("last_success_at"), RefreshSeconds: record.GetInt64("refresh_seconds"), SaveIntervalSeconds: record.GetInt64("save_interval_seconds"), UTCValidFrom: record.GetString("utc_valid_from_day"), Quality: record.GetString("quality"), ErrorCode: record.GetString("error_code")}
		v.Sources = append(v.Sources, source)
		if source.Iface == settings.Iface {
			copy := source
			v.Source = &copy
		}
	}
	cycleStart, cycleEnd, err := trafficCycle(now, settings.BillingDay)
	if err != nil || settings.QuotaBytes > traffic.MaxSafeBytes || (settings.CountMode != "tx" && settings.CountMode != "rx_tx") || settings.BillingTimezone != "UTC" {
		v.Status = "invalid_config"
		return v, nil
	}
	today := now.UTC().Truncate(24 * time.Hour)
	monthStart := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, time.UTC)
	rowsByDay := make(map[string]trafficDay)
	created, validFrom := int64(0), ""
	if v.Source != nil {
		created, validFrom = v.Source.CreatedAt, v.Source.UTCValidFrom
		v.Quality = v.Source.Quality
		if v.ErrorCode == "" {
			v.ErrorCode = v.Source.ErrorCode
		}
		if v.Source.LastSuccessAt > 0 {
			stamp := time.Unix(v.Source.LastSuccessAt, 0).UTC().Format(time.RFC3339)
			v.LastSuccessAt = &stamp
		}
		threshold := max(int64(900), 3*v.Source.RefreshSeconds, 3*v.Source.SaveIntervalSeconds)
		v.Freshness = "fresh"
		if now.Unix()-v.Source.UpdatedAt > threshold || settingsRecord == nil || now.Unix()-settingsRecord.GetInt64("collector_last_observed_at") > threshold || v.Status != traffic.OK {
			v.Freshness = "stale"
		}
		summaryStart := cycleStart
		if monthStart.Before(summaryStart) {
			summaryStart = monthStart
		}
		var rows []*core.Record
		filter := dbx.NewExp("(day >= {:from} AND day < {:to}) OR (day >= {:summary} AND day <= {:today})", dbx.Params{"from": from.Format(time.DateOnly), "to": to.Format(time.DateOnly), "summary": summaryStart.Format(time.DateOnly), "today": today.Format(time.DateOnly)})
		if err := app.RecordQuery("traffic_daily").WithContext(ctx).AndWhere(dbx.HashExp{"system": id, "iface": settings.Iface}).AndWhere(filter).OrderBy("day").Limit(732).All(&rows); err != nil {
			return v, err
		}
		for _, row := range rows {
			rx, tx := uint64(row.GetInt64("rx_bytes")), uint64(row.GetInt64("tx_bytes"))
			rowsByDay[row.GetString("day")] = trafficDay{Day: row.GetString("day"), RxBytes: &rx, TxBytes: &tx, Quality: row.GetString("quality")}
		}
	} else if settings.Iface == "" && len(v.Sources) > 0 {
		v.Status = "unconfigured"
	}
	if !settings.Enabled {
		v.Status = traffic.Disabled
	}
	if v.Today, err = aggregateTraffic(now, today, today.AddDate(0, 0, 1), settings, rowsByDay, created, validFrom); err != nil {
		v.Status = "numeric_overflow"
		return v, nil
	}
	if v.CalendarMonth, err = aggregateTraffic(now, monthStart, monthStart.AddDate(0, 1, 0), settings, rowsByDay, created, validFrom); err != nil {
		v.Status = "numeric_overflow"
		return v, nil
	}
	if v.BillingCycle, err = aggregateTraffic(now, cycleStart, cycleEnd, settings, rowsByDay, created, validFrom); err != nil {
		v.Status = "numeric_overflow"
		return v, nil
	}
	v.trafficSummary = v.BillingCycle
	if v.Source != nil {
		for day := from; day.Before(to) && !day.After(today); day = day.AddDate(0, 0, 1) {
			key := day.Format(time.DateOnly)
			value, ok := rowsByDay[key]
			if !ok || key < validFrom {
				value = trafficDay{Day: key, Quality: "missing"}
			}
			v.Days = append(v.Days, value)
		}
	}
	return v, nil
}

func (h *Hub) trafficOverview(e *core.RequestEvent) error {
	e.Response.Header().Set("Cache-Control", "no-store")
	query := e.Request.URL.Query()
	limit, offset := 50, 0
	var err error
	if raw := query.Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 50 {
			return e.BadRequestError("limit must be 1..50", nil)
		}
	}
	if raw := query.Get("offset"); raw != "" {
		offset, err = strconv.Atoi(raw)
		if err != nil || offset < 0 || offset > 100000 {
			return e.BadRequestError("invalid offset", nil)
		}
	}
	ctx, cancel := context.WithTimeout(e.Request.Context(), 5*time.Second)
	defer cancel()
	q := e.App.RecordQuery("systems").WithContext(ctx).OrderBy("id").Limit(int64(limit + 1)).Offset(int64(offset))
	if share, _ := utils.GetEnv("SHARE_ALL_SYSTEMS"); share != "true" {
		q.AndWhere(dbx.NewExp("EXISTS (SELECT 1 FROM json_each(systems.users) WHERE value={:user})", dbx.Params{"user": e.Auth.Id}))
	}
	var systems []*core.Record
	if err := q.All(&systems); err != nil {
		return err
	}
	hasMore := len(systems) > limit
	if hasMore {
		systems = systems[:limit]
	}
	now := time.Now().UTC()
	items := make([]trafficView, 0, len(systems))
	for _, system := range systems {
		v, err := buildTrafficView(ctx, e.App, system, now, now.Truncate(24*time.Hour), now.Truncate(24*time.Hour).AddDate(0, 0, 1))
		if err != nil {
			return err
		}
		// Detail history is not sent to the overview; one response for all systems.
		v.Days = nil
		items = append(items, v)
	}
	return e.JSON(http.StatusOK, map[string]any{"generated_at": now.Format(time.RFC3339), "items": items, "has_more": hasMore})
}

func (h *Hub) trafficDetail(e *core.RequestEvent) error {
	e.Response.Header().Set("Cache-Control", "no-store")
	system, err := authorizedTrafficSystem(e)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	today := now.Truncate(24 * time.Hour)
	from, to := today.AddDate(0, 0, -29), today.AddDate(0, 0, 1)
	if raw := e.Request.URL.Query().Get("from"); raw != "" {
		from, err = traffic.ParseDay(raw)
		if err != nil {
			return e.BadRequestError("invalid from date", nil)
		}
	}
	if raw := e.Request.URL.Query().Get("to"); raw != "" {
		to, err = traffic.ParseDay(raw)
		if err != nil {
			return e.BadRequestError("invalid to date", nil)
		}
	}
	if !from.Before(to) || to.Sub(from) > 366*24*time.Hour || from.After(today) {
		return e.BadRequestError("date range must be 1..366 UTC days", nil)
	}
	ctx, cancel := context.WithTimeout(e.Request.Context(), 5*time.Second)
	defer cancel()
	v, err := buildTrafficView(ctx, e.App, system, now, from, to)
	if err != nil {
		return err
	}
	return e.JSON(http.StatusOK, v)
}

func (h *Hub) trafficUpdateSettings(e *core.RequestEvent) error {
	e.Response.Header().Set("Cache-Control", "no-store")
	system, err := authorizedTrafficSystem(e)
	if err != nil {
		return err
	}
	var request struct {
		Iface      *string `json:"iface"`
		Quota      *uint64 `json:"quota_bytes"`
		Mode       *string `json:"count_mode"`
		BillingDay *int    `json:"billing_day"`
		Timezone   *string `json:"billing_timezone"`
		Enabled    *bool   `json:"enabled"`
		Unit       *string `json:"display_unit"`
	}
	raw, err := io.ReadAll(io.LimitReader(e.Request.Body, 4097))
	if err != nil || len(raw) > 4096 {
		return e.BadRequestError("invalid settings payload", nil)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return e.BadRequestError("invalid settings fields", nil)
	}
	if decoder.Decode(new(any)) != io.EOF {
		return e.BadRequestError("invalid JSON", nil)
	}
	ctx, cancel := context.WithTimeout(e.Request.Context(), 3*time.Second)
	defer cancel()
	err = e.App.RunInTransaction(func(tx core.App) error {
		current, err := tx.FindRecordById("systems", system.Id)
		if err != nil || !canAccessTrafficSystem(e.Auth, current) {
			return e.NotFoundError("System not found", nil)
		}
		record, err := firstTrafficRecord(tx, "traffic_settings", system.Id)
		if err != nil {
			return err
		}
		settings := settingsFromRecord(system.Id, record)
		if request.Iface != nil {
			if !traffic.ValidInterface(*request.Iface) {
				return e.BadRequestError("invalid interface", nil)
			}
			_, err := tx.FindFirstRecordByFilter("traffic_sources", "system={:system} && iface={:iface}", dbx.Params{"system": system.Id, "iface": *request.Iface})
			if err != nil {
				return e.BadRequestError("unknown interface", nil)
			}
			settings.Iface = *request.Iface
		}
		if request.Quota != nil {
			if *request.Quota > traffic.MaxSafeBytes {
				return e.BadRequestError("quota exceeds safe integer range", nil)
			}
			settings.QuotaBytes = *request.Quota
		}
		if request.Mode != nil {
			if *request.Mode != "tx" && *request.Mode != "rx_tx" {
				return e.BadRequestError("invalid count mode", nil)
			}
			settings.CountMode = *request.Mode
		}
		if request.BillingDay != nil {
			if *request.BillingDay < 1 || *request.BillingDay > 28 {
				return e.BadRequestError("billing day must be 1..28", nil)
			}
			settings.BillingDay = *request.BillingDay
		}
		if request.Timezone != nil {
			if *request.Timezone != "UTC" {
				return e.BadRequestError("only UTC is supported", nil)
			}
			settings.BillingTimezone = *request.Timezone
		}
		if request.Enabled != nil {
			settings.Enabled = *request.Enabled
		}
		if request.Unit != nil {
			if *request.Unit != "GB" && *request.Unit != "GiB" {
				return e.BadRequestError("invalid display unit", nil)
			}
			settings.DisplayUnit = *request.Unit
		}
		if record == nil {
			collection, err := tx.FindCachedCollectionByNameOrId("traffic_settings")
			if err != nil {
				return err
			}
			record = core.NewRecord(collection)
		}
		// Explicit whitelist. Collector status, counters and ownership cannot be changed.
		record.Set("system", system.Id)
		record.Set("iface", settings.Iface)
		record.Set("quota_bytes", settings.QuotaBytes)
		record.Set("count_mode", settings.CountMode)
		record.Set("billing_day", settings.BillingDay)
		record.Set("billing_timezone", settings.BillingTimezone)
		record.Set("display_unit", settings.DisplayUnit)
		record.Set("enabled", settings.Enabled)
		return tx.SaveWithContext(ctx, record)
	})
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	today := now.Truncate(24 * time.Hour)
	v, err := buildTrafficView(e.Request.Context(), e.App, system, now, today, today.AddDate(0, 0, 1))
	if err != nil {
		return err
	}
	return e.JSON(http.StatusOK, v)
}
