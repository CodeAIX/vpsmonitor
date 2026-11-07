package system

import (
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHistoryPerCoreJSONRoundTrip(t *testing.T) {
	sample := HistorySample{At: 1790000000000, Stats: Stats{CpuCoresUsage: Uint8Slice{0, 42, 100, 255}, Battery: Battery{55, 1}}}
	b, err := json.Marshal(sample)
	require.NoError(t, err)
	var legacy, strict HistorySample
	require.NoError(t, json.Unmarshal(b, &legacy))
	require.NoError(t, jsonv2.Unmarshal(b, &strict))
	require.Equal(t, sample.Stats.CpuCoresUsage, legacy.Stats.CpuCoresUsage)
	require.Equal(t, sample.Stats.CpuCoresUsage, strict.Stats.CpuCoresUsage)
	require.Equal(t, sample.Stats.Battery, strict.Stats.Battery)
	var cores Uint8Slice
	require.NoError(t, jsonv2.Unmarshal([]byte(`"ACpk/w=="`), &cores))
	require.Equal(t, Uint8Slice{0, 42, 100, 255}, cores)
	require.Error(t, jsonv2.Unmarshal([]byte(`[256]`), &cores))
	require.Error(t, jsonv2.Unmarshal([]byte(`[-1]`), &cores))
}
