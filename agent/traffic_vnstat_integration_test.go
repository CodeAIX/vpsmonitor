//go:build testing

package agent

import (
	"context"
	"os"
	"os/exec"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fxamacker/cbor/v2"
	"github.com/CodeAIX/vpsmonitor/internal/common"
	"github.com/CodeAIX/vpsmonitor/internal/entities/system"
	"github.com/CodeAIX/vpsmonitor/internal/entities/traffic"
	"github.com/stretchr/testify/require"
)

func TestTrafficGetDataUsesCacheWhileCLIBlocked(t *testing.T) {
	if !trafficPlatformSupported() {
		t.Skip("Linux collector integration")
	}
	a := createTestAgent(t)
	a.gatherStats(common.DataRequestOptions{CacheTimeMs: defaultDataCacheTimeMs})
	entered := make(chan struct{})
	var calls atomic.Int32
	c := testTrafficCollector(func(ctx context.Context, _ string, _ ...string) ([]byte, string) {
		calls.Add(1)
		close(entered)
		<-ctx.Done()
		return nil, traffic.Timeout
	})
	c.publishError(traffic.Initializing, time.Now())
	a.trafficCollector = c
	stop := c.start(t.Context())
	defer stop()
	<-entered
	options, err := cbor.Marshal(common.DataRequestOptions{CacheTimeMs: defaultDataCacheTimeMs})
	require.NoError(t, err)
	start := time.Now()
	for range 10 {
		hctx := &HandlerContext{Agent: a, Request: &common.HubRequest[cbor.RawMessage]{Data: options}, SendResponse: func(value any, _ *uint32) error {
			data := value.(*system.CombinedData)
			require.NotNil(t, data.Traffic)
			require.Equal(t, traffic.Initializing, data.Traffic.Status)
			return nil
		}}
		require.NoError(t, (&GetDataHandler{}).Handle(hctx))
	}
	require.Less(t, time.Since(start), time.Second, "GetData must not wait for the 3s CLI timeout")
	require.Equal(t, int32(1), calls.Load(), "cached requests must not launch more CLI processes")
}

// Opt-in real CLI/readonly-volume test, executed inside the local Linux image.
func TestTrafficDockerReadOnlyCLI(t *testing.T) {
	if os.Getenv("VPSMONITOR_TRAFFIC_INTEGRATION") != "true" {
		t.Skip("requires isolated vnstatd volume")
	}
	require.True(t, trafficPlatformSupported())
	require.NotZero(t, os.Geteuid(), "CLI proof must be non-root")
	file, err := os.OpenFile("/var/lib/vnstat/.plus-write-check", os.O_CREATE|os.O_WRONLY, 0600)
	if file != nil {
		file.Close()
		os.Remove("/var/lib/vnstat/.plus-write-check")
	}
	require.Error(t, err, "vnStat data volume must be mounted readonly")
	path, err := exec.LookPath("vnstat")
	require.NoError(t, err)
	c := testTrafficCollector(runVNStatCommand)
	c.cfg.path = path
	c.cfg.config = "/etc/vnstat.conf"
	c.cfg.validFrom = time.Now().UTC().Format(time.DateOnly)
	c.refresh(t.Context(), time.Now())
	s := c.current.Load()
	require.Equal(t, traffic.OK, s.Status)
	require.NotEmpty(t, s.Interfaces)
	require.NotEmpty(t, s.Interfaces[0].Days)
	require.NoError(t, s.Validate(time.Now()))
	t.Logf("real CLI version=%s interfaces=%d source_updated=%d", s.VNStatVersion, len(s.Interfaces), s.Interfaces[0].SourceUpdatedAt)
}
