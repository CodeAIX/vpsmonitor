package hub

import (
	"testing"
	"time"

	"github.com/CodeAIX/vpsmonitor/internal/entities/traffic"
	"github.com/stretchr/testify/require"
)

func TestTrafficBillingBoundaries(t *testing.T) {
	for _, tc := range []struct {
		now        string
		day        int
		start, end string
	}{
		{"2026-11-05", 1, "2026-11-01", "2026-12-01"},
		{"2026-11-05", 5, "2026-11-05", "2026-12-05"},
		{"2026-11-05", 15, "2026-10-15", "2026-11-15"},
		{"2026-03-01", 28, "2026-02-28", "2026-03-28"},
		{"2028-02-29", 28, "2028-02-28", "2028-03-28"},
	} {
		t.Run(tc.now+tc.start, func(t *testing.T) {
			now, err := traffic.ParseDay(tc.now)
			require.NoError(t, err)
			start, end, err := trafficCycle(now.Add(12*time.Hour), tc.day)
			require.NoError(t, err)
			require.Equal(t, tc.start, start.Format(time.DateOnly))
			require.Equal(t, tc.end, end.Format(time.DateOnly))
		})
	}
	for _, day := range []int{0, 29, 31} {
		_, _, err := trafficCycle(time.Now(), day)
		require.Error(t, err)
	}
}

func TestTrafficAggregationQuotaCoverageAndPrecision(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)
	rx, tx := uint64(100_000_000_000), uint64(200_000_000_000)
	days := map[string]trafficDay{"2026-11-06": {Day: "2026-11-06", RxBytes: &rx, TxBytes: &tx, Quality: "normal"}}
	settings := defaultTrafficSettings("s")
	settings.QuotaBytes = 1_000_000_000_000
	result, err := aggregateTraffic(now, start, end, settings, days, start.AddDate(0, -1, 0).Unix(), "2026-10-01")
	require.NoError(t, err)
	require.Equal(t, "partial", result.Coverage)
	require.Len(t, result.MissingDays, 5)
	require.Equal(t, 20.0, *result.Percent)
	require.Equal(t, tx, *result.UsedBytes)
	require.True(t, result.Ongoing)
	settings.CountMode = "rx_tx"
	result, err = aggregateTraffic(now, start, end, settings, days, 0, "2026-10-01")
	require.NoError(t, err)
	require.Equal(t, 30.0, *result.Percent)
	settings.QuotaBytes = 0
	result, err = aggregateTraffic(now, start, end, settings, days, 0, "2026-10-01")
	require.NoError(t, err)
	require.Nil(t, result.Percent)
	settings.QuotaBytes = 250_000_000_000
	result, err = aggregateTraffic(now, start, end, settings, days, 0, "2026-10-01")
	require.NoError(t, err)
	require.Equal(t, 120.0, *result.Percent)
	result, err = aggregateTraffic(now, start, end, settings, nil, 0, "2026-10-01")
	require.NoError(t, err)
	require.Equal(t, "unavailable", result.Coverage)
	require.Nil(t, result.UsedBytes)
	// Exactly zero is valid when explicitly collected, unlike a missing day.
	zero := uint64(0)
	days = map[string]trafficDay{}
	for day := start; !day.After(now); day = day.AddDate(0, 0, 1) {
		key := day.Format(time.DateOnly)
		days[key] = trafficDay{Day: key, RxBytes: &zero, TxBytes: &zero, Quality: "normal"}
	}
	result, err = aggregateTraffic(now, start, end, settings, days, 0, "2026-10-01")
	require.NoError(t, err)
	require.Equal(t, "unknown", result.Coverage)
	require.Empty(t, result.MissingDays)
	require.NotNil(t, result.UsedBytes)
	require.Zero(t, *result.UsedBytes)
	maxValue := traffic.MaxSafeBytes
	one := uint64(1)
	days["2026-11-01"] = trafficDay{RxBytes: &maxValue, TxBytes: &one, Quality: "normal"}
	_, err = aggregateTraffic(now, start, end, settings, days, 0, "2026-10-01")
	require.Error(t, err)
}
