package hub

import (
	"errors"
	"time"

	"github.com/CodeAIX/vpsmonitor/internal/entities/traffic"
)

type trafficSettings struct {
	System          string `json:"system"`
	Enabled         bool   `json:"enabled"`
	Iface           string `json:"iface"`
	QuotaBytes      uint64 `json:"quota_bytes"`
	CountMode       string `json:"count_mode"`
	BillingDay      int    `json:"billing_day"`
	BillingTimezone string `json:"billing_timezone"`
	DisplayUnit     string `json:"display_unit"`
}

type trafficDay struct {
	Day     string  `json:"day"`
	RxBytes *uint64 `json:"rx_bytes"`
	TxBytes *uint64 `json:"tx_bytes"`
	Quality string  `json:"quality"`
}

type trafficSummary struct {
	PeriodStart string   `json:"period_start"`
	PeriodEnd   string   `json:"period_end_exclusive"`
	RxBytes     *uint64  `json:"rx_bytes"`
	TxBytes     *uint64  `json:"tx_bytes"`
	TotalBytes  *uint64  `json:"total_bytes"`
	UsedBytes   *uint64  `json:"used_bytes"`
	Percent     *float64 `json:"percent"`
	Coverage    string   `json:"coverage"`
	MissingDays []string `json:"missing_days"`
	Ongoing     bool     `json:"ongoing"`
}

func trafficCycle(now time.Time, billingDay int) (start, end time.Time, err error) {
	if billingDay < 1 || billingDay > 28 {
		return start, end, errors.New("billing day must be 1..28")
	}
	now = now.UTC()
	start = time.Date(now.Year(), now.Month(), billingDay, 0, 0, 0, 0, time.UTC)
	if now.Before(start) {
		start = start.AddDate(0, -1, 0)
	}
	return start, start.AddDate(0, 1, 0), nil
}

func safeTrafficAdd(a, b uint64) (uint64, error) {
	if a > traffic.MaxSafeBytes || b > traffic.MaxSafeBytes-a {
		return 0, errors.New("traffic numeric_overflow")
	}
	return a + b, nil
}

func aggregateTraffic(now, start, end time.Time, settings trafficSettings, days map[string]trafficDay, createdAt int64, validFrom string) (trafficSummary, error) {
	result := trafficSummary{PeriodStart: start.Format(time.DateOnly), PeriodEnd: end.Format(time.DateOnly), Coverage: "unavailable", MissingDays: []string{}}
	today := now.UTC().Truncate(24 * time.Hour)
	last := end
	if last.After(today.AddDate(0, 0, 1)) {
		last = today.AddDate(0, 0, 1)
	}
	var rx, tx uint64
	count := 0
	partial := false
	for day := start; day.Before(last); day = day.AddDate(0, 0, 1) {
		key := day.Format(time.DateOnly)
		value, ok := days[key]
		if !ok || value.RxBytes == nil || value.TxBytes == nil || key < validFrom {
			result.MissingDays = append(result.MissingDays, key)
			partial = true
			continue
		}
		var err error
		if rx, err = safeTrafficAdd(rx, *value.RxBytes); err != nil {
			return result, err
		}
		if tx, err = safeTrafficAdd(tx, *value.TxBytes); err != nil {
			return result, err
		}
		count++
		if value.Quality != "normal" {
			partial = true
		}
	}
	if count == 0 {
		return result, nil
	}
	total, err := safeTrafficAdd(rx, tx)
	if err != nil {
		return result, err
	}
	used := tx
	if settings.CountMode == "rx_tx" {
		used = total
	}
	result.RxBytes = &rx
	result.TxBytes = &tx
	result.TotalBytes = &total
	result.UsedBytes = &used
	if settings.QuotaBytes > 0 {
		percent := 100 * float64(used) / float64(settings.QuotaBytes)
		result.Percent = &percent
	}
	result.Ongoing = last.After(today)
	// Daily buckets alone cannot prove daemon continuity within a day. Never
	// claim complete solely because all dates exist (vnstatd may have stopped).
	result.Coverage = "unknown"
	if partial || createdAt > start.Unix() || validFrom > result.PeriodStart {
		result.Coverage = "partial"
	}
	return result, nil
}
