//go:build testing

package agent

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/fxamacker/cbor/v2"
	"github.com/CodeAIX/vpsmonitor/internal/common"
	"github.com/CodeAIX/vpsmonitor/internal/entities/system"
	"github.com/CodeAIX/vpsmonitor/internal/history"
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
