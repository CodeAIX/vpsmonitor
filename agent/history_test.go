//go:build testing

package agent

import (
	"encoding/hex"
	"encoding/json"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/CodeAIX/vpsmonitor/internal/common"
	"github.com/CodeAIX/vpsmonitor/internal/entities/system"
	"github.com/CodeAIX/vpsmonitor/internal/history"
	"github.com/fxamacker/cbor/v2"
	"github.com/stretchr/testify/require"
)

func TestHistoryFreezesMetricsWithoutCapturingLiveState(t *testing.T) {
	s, err := history.Open(t.TempDir(), 0, time.Now())
	require.NoError(t, err)
	h := &agentHistory{store: s, queue: make(chan []byte, 2)}
	data := &system.CombinedData{Stats: system.Stats{Cpu: 42, Temperatures: map[string]float64{"cpu": 50}}, Info: system.Info{AgentVersion: "live-only"}}
	h.enqueue(data)
	data.Stats.Temperatures["cpu"] = 99
	var frozen system.HistorySample
	encoded := <-h.queue
	require.NoError(t, json.Unmarshal(encoded, &frozen))
	require.Equal(t, float64(50), frozen.Stats.Temperatures["cpu"])
	require.NotContains(t, string(encoded), "live-only")
	// A stalled disk worker never blocks the collector; the bounded queue drops
	// a sample with an explicit diagnostic instead of growing memory forever.
	h.enqueue(data)
	h.enqueue(data)
	h.enqueue(data)
	require.Equal(t, "writer_busy", s.Info().Error)
}

func TestHistoryHandlerAuthenticationAndLegacyWireCompatibility(t *testing.T) {
	now := time.Now().UTC()
	s, err := history.Open(t.TempDir(), 0, now)
	require.NoError(t, err)
	require.NoError(t, s.Append(system.HistorySample{At: now.Add(-time.Minute).UnixMilli(), Stats: system.Stats{Cpu: 42}}, now))
	a := &Agent{history: &agentHistory{store: s, queue: make(chan []byte, 2)}}
	options, err := cbor.Marshal(system.HistoryRequest{After: 0, Before: now.UnixMilli(), Limit: 128})
	require.NoError(t, err)
	var received system.HistoryPage
	ctx := &HandlerContext{Agent: a, Request: &common.HubRequest[cbor.RawMessage]{Action: common.GetHistory, Data: options}, SendResponse: func(data any, _ *uint32) error { received = data.(system.HistoryPage); return nil }}
	registry := NewHandlerRegistry()
	require.ErrorContains(t, registry.Handle(ctx), "hub not verified")
	for _, transport := range []system.ConnectionType{system.ConnectionTypeSSH, system.ConnectionTypeWebSocket} {
		ctx.HubVerified = true
		ctx.ConnectionType = transport
		require.NoError(t, registry.Handle(ctx))
		require.Len(t, received.Samples, 1)
		require.Equal(t, float64(42), received.Samples[0].Stats.Cpu)
	}
	daily := system.HistoryRequest{Before: now.UnixMilli(), Limit: system.HistoryDailySamples, Daily: true}
	ctx.Request.Data, err = cbor.Marshal(daily)
	require.NoError(t, err)
	require.NoError(t, registry.Handle(ctx))
	require.Empty(t, received.Samples)
	require.NotEmpty(t, received.Packed)
	require.NoError(t, received.UnpackDaily(daily))
	require.Len(t, received.Samples, 1)
	require.Equal(t, float64(42), received.Samples[0].Stats.Cpu)

	// New optional key 8 is ignored by the old response shape; existing keys and
	// action numbers are unchanged.
	info := s.Info()
	wire, err := cbor.Marshal(system.CombinedData{Stats: system.Stats{Cpu: 12}, History: &info})
	require.NoError(t, err)
	var legacy struct {
		Stats system.Stats `cbor:"0,keyasint"`
		Info  system.Info  `cbor:"1,keyasint"`
	}
	require.NoError(t, cbor.Unmarshal(wire, &legacy))
	require.Equal(t, float64(12), legacy.Stats.Cpu)
	require.EqualValues(t, 9, common.GetSystemdLogs)
	require.EqualValues(t, 10, common.GetHistory)
}

func TestHistorySamplerDoesNotConsumePendingSystemDetails(t *testing.T) {
	cache := NewSystemDataCache()
	cached := &system.CombinedData{}
	cache.Set(cached, defaultDataCacheTimeMs)
	a := &Agent{cache: cache, history: &agentHistory{}, detailsDirty: true, systemDetails: system.Details{Hostname: "sample-host"}}
	local := a.gatherStatsInternal(common.DataRequestOptions{CacheTimeMs: defaultDataCacheTimeMs}, true)
	require.Nil(t, local.Details)
	require.True(t, a.detailsDirty)
	response := a.gatherStats(common.DataRequestOptions{CacheTimeMs: defaultDataCacheTimeMs, IncludeDetails: true})
	require.NotNil(t, response.Details)
	require.Equal(t, "sample-host", response.Details.Hostname)
	require.Nil(t, cached.Details)
	require.False(t, a.detailsDirty)
}

func TestHistoryHandlerSplitsIncompressibleDailyResponse(t *testing.T) {
	day := time.Now().UTC().Truncate(24 * time.Hour)
	start := day.Add(-time.Hour)
	const count = 16
	s, err := history.Open(t.TempDir(), 0, start)
	require.NoError(t, err)

	// Each sample is below the per-sample limit and all 16 are below the
	// decoded page limit, but fresh high-entropy keys exceed the wire limit.
	rng := rand.NewChaCha8([32]byte{17})
	for n := range count {
		temperatures := make(map[string]float64, 80)
		for range 80 {
			var key [1024]byte
			_, err := rng.Read(key[:])
			require.NoError(t, err)
			temperatures[hex.EncodeToString(key[:])] = 42
		}
		at := start.Add(time.Duration(n) * time.Minute)
		require.NoError(t, s.Append(system.HistorySample{
			At: at.UnixMilli(), Stats: system.Stats{Cpu: float64(n), Temperatures: temperatures},
		}, at))
	}
	var received system.HistoryPage
	ctx := &HandlerContext{
		Agent:          &Agent{history: &agentHistory{store: s}},
		Request:        &common.HubRequest[cbor.RawMessage]{Action: common.GetHistory},
		HubVerified:    true,
		ConnectionType: system.ConnectionTypeWebSocket,
		SendResponse: func(data any, _ *uint32) error {
			received = data.(system.HistoryPage)
			return nil
		},
	}
	registry := NewHandlerRegistry()
	req := system.HistoryRequest{Before: day.UnixMilli(), Daily: true, Limit: system.HistoryDailySamples}
	var cpus []float64
	pages := 0
	for {
		require.Less(t, pages, count)
		ctx.Request.Data, err = cbor.Marshal(req)
		require.NoError(t, err)
		require.NoError(t, registry.Handle(ctx))
		wire, err := cbor.Marshal(received)
		require.NoError(t, err)
		require.LessOrEqual(t, len(wire), system.HistoryMaxPageBytes)
		require.Greater(t, received.Next, req.After)
		require.NoError(t, received.UnpackDaily(req))
		for _, sample := range received.Samples {
			cpus = append(cpus, sample.Stats.Cpu)
		}
		pages++
		if !received.More {
			break
		}
		req.After = received.Next
	}
	require.Greater(t, pages, 1)
	require.Len(t, cpus, count)
	for n, cpu := range cpus {
		require.Equal(t, float64(n), cpu)
	}
}
