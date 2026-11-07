package system

import (
	"testing"
	"time"

	"github.com/fxamacker/cbor/v2"
	"github.com/CodeAIX/vpsmonitor/internal/entities/traffic"
	"github.com/stretchr/testify/require"
)

func TestOptionalTrafficWireCompatibility(t *testing.T) {
	original := CombinedData{Info: Info{Uptime: 42}, Stats: Stats{Cpu: 12.5}}
	encoded, err := cbor.Marshal(original)
	require.NoError(t, err)
	var keys map[int]cbor.RawMessage
	require.NoError(t, cbor.Unmarshal(encoded, &keys))
	require.NotContains(t, keys, 7)
	var upgraded CombinedData
	require.NoError(t, cbor.Unmarshal(encoded, &upgraded))
	require.Nil(t, upgraded.Traffic)
	original.Traffic = &traffic.Snapshot{SchemaVersion: 1, Status: traffic.NoDatabase, ErrorCode: traffic.NoDatabase, ObservedAt: time.Now().Unix(), RefreshSeconds: 300}
	encoded, err = cbor.Marshal(original)
	require.NoError(t, err)
	require.NoError(t, cbor.Unmarshal(encoded, &keys))
	require.Contains(t, keys, 7)
	// Exact pre-extension outer contract, including all original key numbers.
	type legacyData struct {
		Stats      Stats           `cbor:"0,keyasint"`
		Info       Info            `cbor:"1,keyasint"`
		Containers cbor.RawMessage `cbor:"2,keyasint"`
		Systemd    cbor.RawMessage `cbor:"3,keyasint"`
		Details    cbor.RawMessage `cbor:"4,keyasint"`
		Updated    bool            `cbor:"5,keyasint"`
		Monitors   cbor.RawMessage `cbor:"6,keyasint"`
	}
	var legacy legacyData
	require.NoError(t, cbor.Unmarshal(encoded, &legacy))
	require.Equal(t, uint64(42), legacy.Info.Uptime)
	require.Equal(t, 12.5, legacy.Stats.Cpu)
}
