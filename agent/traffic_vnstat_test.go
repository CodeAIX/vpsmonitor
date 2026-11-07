package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/CodeAIX/vpsmonitor/internal/entities/traffic"
	"github.com/stretchr/testify/require"
)

var trafficTestNow = time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)

func trafficFixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("test-data", "vnstat", name+".json"))
	require.NoError(t, err)
	return raw
}

func testTrafficCollector(run vnStatRunner) *vnStatCollector {
	return &vnStatCollector{cfg: vnStatConfig{path: "/test/vnstat", config: "/test/vnstat.conf", validFrom: "2026-11-06", refresh: 5 * time.Minute}, run: run}
}

func TestTrafficRealFixtures(t *testing.T) {
	for _, name := range []string{"debian12", "debian13", "ubuntu24"} {
		t.Run(name, func(t *testing.T) {
			raw := trafficFixture(t, name)
			c := testTrafficCollector(func(_ context.Context, _ string, args ...string) ([]byte, string) {
				if args[len(args)-1] == "--showconfig" {
					return []byte("UseUTC 1\n;SaveInterval 5\n"), ""
				}
				return raw, ""
			})
			c.refresh(t.Context(), trafficTestNow)
			s := c.current.Load()
			require.Equal(t, traffic.OK, s.Status)
			require.NoError(t, s.Validate(trafficTestNow))
			require.Equal(t, "2026-11-06", s.Interfaces[0].Days[0].Day)
			var original struct {
				Interfaces []struct {
					Traffic struct{ Day []struct{ Rx, Tx uint64 } }
				}
			}
			require.NoError(t, json.Unmarshal(raw, &original))
			require.Equal(t, original.Interfaces[0].Traffic.Day[0].Rx, s.Interfaces[0].Days[0].RxBytes)
			require.Equal(t, original.Interfaces[0].Traffic.Day[0].Tx, s.Interfaces[0].Days[0].TxBytes)
		})
	}
}

func mutatedTrafficFixture(t *testing.T, mutate func(map[string]any)) []byte {
	t.Helper()
	var data map[string]any
	require.NoError(t, json.Unmarshal(trafficFixture(t, "debian12"), &data))
	mutate(data)
	raw, err := json.Marshal(data)
	require.NoError(t, err)
	return raw
}

func sourceOf(data map[string]any) map[string]any {
	return data["interfaces"].([]any)[0].(map[string]any)
}
func dailyOf(data map[string]any) map[string]any {
	return sourceOf(data)["traffic"].(map[string]any)["day"].([]any)[0].(map[string]any)
}

func TestTrafficUntrustedInputs(t *testing.T) {
	cases := []struct {
		name, want string
		mutate     func(map[string]any)
	}{
		{"missing_interfaces", traffic.InvalidJSON, func(d map[string]any) { delete(d, "interfaces") }},
		{"empty_interfaces", traffic.NoInterfaces, func(d map[string]any) { d["interfaces"] = []any{} }},
		{"missing_day", traffic.InvalidJSON, func(d map[string]any) { delete(sourceOf(d)["traffic"].(map[string]any), "day") }},
		{"missing_rx", traffic.InvalidJSON, func(d map[string]any) { delete(dailyOf(d), "rx") }},
		{"negative", traffic.InvalidJSON, func(d map[string]any) { dailyOf(d)["tx"] = -1 }},
		{"unsafe_integer", traffic.InvalidJSON, func(d map[string]any) { dailyOf(d)["rx"] = traffic.MaxSafeBytes + 1 }},
		{"unsafe_sum", traffic.InvalidJSON, func(d map[string]any) { dailyOf(d)["rx"] = traffic.MaxSafeBytes; dailyOf(d)["tx"] = 1 }},
		{"fractional", traffic.InvalidJSON, func(d map[string]any) { dailyOf(d)["tx"] = 1.2 }},
		{"invalid_date", traffic.InvalidJSON, func(d map[string]any) { dailyOf(d)["date"] = map[string]any{"year": 2026, "month": 2, "day": 30} }},
		{"future_day", traffic.InvalidJSON, func(d map[string]any) {
			dailyOf(d)["date"] = map[string]any{"year": 2026, "month": 11, "day": 7}
			dailyOf(d)["timestamp"] = trafficTestNow.AddDate(0, 0, 1).Truncate(24 * time.Hour).Unix()
		}},
		{"local_boundary", traffic.UnsupportedTimebase, func(d map[string]any) { dailyOf(d)["timestamp"] = float64(1793923200 - 8*3600) }},
		{"future_updated", traffic.InvalidJSON, func(d map[string]any) {
			sourceOf(d)["updated"].(map[string]any)["timestamp"] = trafficTestNow.Unix() + 301
		}},
		{"unknown_schema", traffic.UnsupportedVersion, func(d map[string]any) { d["jsonversion"] = "999" }},
		{"unknown_version", traffic.UnsupportedVersion, func(d map[string]any) { d["vnstatversion"] = "9.99" }},
		{"bad_name", traffic.InvalidJSON, func(d map[string]any) { sourceOf(d)["name"] = "eth0\nsecret" }},
		{"duplicate_day", traffic.InvalidJSON, func(d map[string]any) { sourceOf(d)["traffic"].(map[string]any)["day"] = []any{dailyOf(d), dailyOf(d)} }},
		{"duplicate_iface", traffic.InvalidJSON, func(d map[string]any) { d["interfaces"] = []any{sourceOf(d), sourceOf(d)} }},
		{"too_many_interfaces", traffic.TooLarge, func(d map[string]any) {
			original := sourceOf(d)
			values := []any{}
			for range 17 {
				values = append(values, original)
			}
			d["interfaces"] = values
		}},
		{"too_many_days", traffic.TooLarge, func(d map[string]any) {
			original := dailyOf(d)
			values := []any{}
			for range 401 {
				values = append(values, original)
			}
			sourceOf(d)["traffic"].(map[string]any)["day"] = values
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := mutatedTrafficFixture(t, tc.mutate)
			c := testTrafficCollector(func(_ context.Context, _ string, args ...string) ([]byte, string) {
				if args[len(args)-1] == "--showconfig" {
					return []byte("UseUTC 1\nSaveInterval 5"), ""
				}
				return raw, ""
			})
			c.refresh(t.Context(), trafficTestNow)
			require.Equal(t, tc.want, c.current.Load().Status)
			require.Empty(t, c.current.Load().Interfaces)
		})
	}
	for _, raw := range []string{"", "null", "{}", "[]", "{", "{} {}", strings.Repeat("x", traffic.MaxPayload+1)} {
		_, code := parseVNStatDaily([]byte(raw), trafficTestNow, "2026-11-06")
		require.NotEmpty(t, code)
	}
}

func TestTrafficSeparateInterfacesAndHistoricalCutoff(t *testing.T) {
	raw := mutatedTrafficFixture(t, func(d map[string]any) {
		base := sourceOf(d)
		sources := []any{}
		for _, name := range []string{"eth0", "tailscale0", "docker0"} {
			copy := map[string]any{}
			for key, value := range base {
				copy[key] = value
			}
			copy["name"] = name
			sources = append(sources, copy)
		}
		d["interfaces"] = sources
	})
	s, code := parseVNStatDaily(raw, trafficTestNow, "2026-11-06")
	require.Empty(t, code)
	require.Len(t, s.Interfaces, 3)
	for _, iface := range s.Interfaces {
		require.Equal(t, uint64(70), iface.Days[0].RxBytes)
	}
	// Changing UTC today cannot legitimize an older local-time bucket.
	s, code = parseVNStatDaily(raw, trafficTestNow.AddDate(0, 0, 1), "2026-11-07")
	require.Empty(t, code)
	require.Empty(t, s.Interfaces[0].Days)
}

func TestTrafficConfigAndFixedArgv(t *testing.T) {
	for _, raw := range []string{"UseUTC 0\nSaveInterval 5", ";UseUTC 0\n;SaveInterval 5", "SaveInterval 5", "UseUTC 1\nSaveInterval 0"} {
		_, valid := vnStatUTCConfig([]byte(raw))
		require.False(t, valid)
	}
	var calls [][]string
	c := testTrafficCollector(func(_ context.Context, path string, args ...string) ([]byte, string) {
		require.Equal(t, "/test/vnstat", path)
		calls = append(calls, append([]string{}, args...))
		if len(calls) == 1 {
			return []byte("UseUTC 1\n;SaveInterval 5"), ""
		}
		return trafficFixture(t, "debian12"), ""
	})
	c.refresh(t.Context(), trafficTestNow)
	require.Equal(t, [][]string{{"--config", "/test/vnstat.conf", "--showconfig"}, {"--config", "/test/vnstat.conf", "--json", "d", "--limit", "0"}}, calls)
	t.Setenv("VPSMONITOR_TRAFFIC_ENABLED", "false")
	require.Nil(t, newTrafficCollector())
	t.Setenv("VPSMONITOR_TRAFFIC_ENABLED", "true")
	t.Setenv("VPSMONITOR_TRAFFIC_REFRESH", "garbage")
	t.Setenv("VPSMONITOR_TRAFFIC_UTC_VALID_FROM", "2026-11-06")
	t.Setenv("VPSMONITOR_TRAFFIC_VNSTAT_PATH", "relative")
	c = newTrafficCollector()
	require.Equal(t, 5*time.Minute, c.cfg.refresh)
	require.Equal(t, traffic.InvalidConfig, c.cfg.errorCode)
}

func TestTrafficFailedRefreshClearsCurrentPayload(t *testing.T) {
	var fail bool
	c := testTrafficCollector(func(_ context.Context, _ string, args ...string) ([]byte, string) {
		if fail {
			return nil, traffic.Timeout
		}
		if args[len(args)-1] == "--showconfig" {
			return []byte("UseUTC 1\nSaveInterval 5"), ""
		}
		return trafficFixture(t, "debian12"), ""
	})
	c.refresh(t.Context(), trafficTestNow)
	good := c.current.Load()
	require.Equal(t, traffic.OK, good.Status)
	fail = true
	c.refresh(t.Context(), trafficTestNow.Add(time.Minute))
	require.Equal(t, traffic.Timeout, c.current.Load().Status)
	require.Empty(t, c.current.Load().Interfaces)
	require.Len(t, good.Interfaces, 1, "previous immutable snapshot must not be mutated")
}

func TestTrafficRejectsUnverifiedUTCBeforeQuery(t *testing.T) {
	for _, config := range []string{";UseUTC 0\n;SaveInterval 5", "SaveInterval 5", "UseUTC 1\nSaveInterval 0"} {
		calls := 0
		c := testTrafficCollector(func(_ context.Context, _ string, args ...string) ([]byte, string) {
			calls++
			require.Equal(t, "--showconfig", args[len(args)-1])
			return []byte(config), ""
		})
		c.refresh(t.Context(), trafficTestNow)
		require.Equal(t, traffic.UnsupportedTimebase, c.current.Load().Status)
		require.Empty(t, c.current.Load().Interfaces)
		require.Equal(t, 1, calls, "unverified config must prevent the JSON query")
	}
}

func TestTrafficWorkerDoesNotBlockReadersAndJoins(t *testing.T) {
	if !trafficPlatformSupported() {
		t.Skip("Linux worker integration is run in Docker")
	}
	var calls atomic.Int32
	entered := make(chan struct{})
	c := testTrafficCollector(func(ctx context.Context, _ string, _ ...string) ([]byte, string) {
		calls.Add(1)
		close(entered)
		<-ctx.Done()
		return nil, traffic.Timeout
	})
	c.publishError(traffic.Initializing, time.Now())
	stop := c.start(t.Context())
	<-entered
	var readers sync.WaitGroup
	for range 16 {
		readers.Go(func() {
			for range 1000 {
				require.NotNil(t, c.current.Load())
			}
		})
	}
	readers.Wait()
	require.Equal(t, int32(1), calls.Load())
	started := time.Now()
	stop()
	require.Less(t, time.Since(started), time.Second)
}

func TestVNStatCommandProcessHelper(t *testing.T) {
	args := os.Args
	if len(args) < 3 || args[len(args)-2] != "vnstat-helper" {
		return
	}
	switch args[len(args)-1] {
	case "stdout":
		fmt.Print(strings.Repeat("x", traffic.MaxPayload+1))
	case "stderr":
		fmt.Fprint(os.Stderr, strings.Repeat("s", 8193))
	case "timeout":
		time.Sleep(30 * time.Second)
	case "permission":
		fmt.Fprint(os.Stderr, "Permission denied SECRET")
		os.Exit(1)
	case "database":
		fmt.Print("No database found SECRET")
		os.Exit(1)
	case "interfaces":
		fmt.Print("No interfaces found SECRET")
		os.Exit(1)
	}
	os.Exit(0)
}

func TestVNStatCommandLimits(t *testing.T) {
	for _, tc := range []struct{ mode, want string }{{"stdout", traffic.TooLarge}, {"stderr", traffic.TooLarge}, {"timeout", traffic.Timeout}, {"permission", traffic.PermissionDenied}, {"database", traffic.NoDatabase}, {"interfaces", traffic.NoInterfaces}} {
		t.Run(tc.mode, func(t *testing.T) {
			start := time.Now()
			out, code := runVNStatCommand(t.Context(), os.Args[0], "-test.run=^TestVNStatCommandProcessHelper$", "vnstat-helper", tc.mode)
			require.Equal(t, tc.want, code)
			require.Nil(t, out)
			require.Less(t, time.Since(start), 4*time.Second)
		})
	}
	_, code := runVNStatCommand(t.Context(), filepath.Join(t.TempDir(), "missing"))
	require.Equal(t, traffic.Missing, code)
	path := filepath.Join(t.TempDir(), "not-executable")
	require.NoError(t, os.WriteFile(path, []byte("x"), 0600))
	_, code = runVNStatCommand(t.Context(), path)
	require.Equal(t, traffic.PermissionDenied, code)
}
