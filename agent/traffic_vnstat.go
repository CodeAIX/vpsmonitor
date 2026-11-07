package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/CodeAIX/vpsmonitor/internal/entities/traffic"
)

type vnStatConfig struct {
	path, config, validFrom, errorCode string
	refresh                            time.Duration
}

type vnStatRunner func(context.Context, string, ...string) ([]byte, string)

type vnStatCollector struct {
	cfg     vnStatConfig
	current atomic.Pointer[traffic.Snapshot]
	run     vnStatRunner
}

func newTrafficCollector() *vnStatCollector {
	if os.Getenv("VPSMONITOR_TRAFFIC_ENABLED") != "true" {
		return nil
	}
	cfg := vnStatConfig{path: os.Getenv("VPSMONITOR_TRAFFIC_VNSTAT_PATH"), config: os.Getenv("VPSMONITOR_TRAFFIC_VNSTAT_CONFIG"), validFrom: os.Getenv("VPSMONITOR_TRAFFIC_UTC_VALID_FROM"), refresh: 5 * time.Minute}
	if raw := os.Getenv("VPSMONITOR_TRAFFIC_REFRESH"); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d >= time.Minute && d <= time.Hour {
			cfg.refresh = d
		} else {
			slog.Warn("Invalid VPSMONITOR_TRAFFIC_REFRESH; using 5m")
		}
	}
	if cfg.config == "" {
		cfg.config = "/etc/vnstat.conf"
	}
	if !filepath.IsAbs(cfg.config) || (cfg.path != "" && !filepath.IsAbs(cfg.path)) {
		cfg.errorCode = traffic.InvalidConfig
	}
	if _, err := traffic.ParseDay(cfg.validFrom); err != nil {
		cfg.errorCode = traffic.UnsupportedTimebase
	}
	if base := os.Getenv("VPSMONITOR_TRAFFIC_TIMEBASE"); base != "" && base != "utc" {
		cfg.errorCode = traffic.UnsupportedTimebase
	}
	c := &vnStatCollector{cfg: cfg, run: runVNStatCommand}
	c.publishError(traffic.Initializing, time.Now())
	return c
}

func (c *vnStatCollector) publishError(code string, now time.Time) {
	c.current.Store(&traffic.Snapshot{SchemaVersion: traffic.SchemaVersion, Status: code, ErrorCode: code, ObservedAt: now.Unix(), Timebase: "utc", RefreshSeconds: int64(c.cfg.refresh / time.Second)})
}

// start owns one worker; all GetData requests only load an immutable snapshot.
func (c *vnStatCollector) start(parent context.Context) func() {
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	go func() {
		defer close(done)
		if !trafficPlatformSupported() {
			c.publishError(traffic.UnsupportedPlatform, time.Now())
			return
		}
		ticker := time.NewTicker(c.cfg.refresh)
		defer ticker.Stop()
		for {
			c.refresh(ctx, time.Now())
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return func() { cancel(); <-done }
}

func (c *vnStatCollector) refresh(ctx context.Context, now time.Time) {
	if c.cfg.errorCode != "" {
		c.publishError(c.cfg.errorCode, now)
		return
	}
	path := c.cfg.path
	if path == "" {
		var err error
		path, err = exec.LookPath("vnstat")
		if err != nil {
			c.publishError(traffic.Missing, now)
			return
		}
	}
	args := []string{"--config", c.cfg.config}
	config, code := c.run(ctx, path, append(args[:len(args):len(args)], "--showconfig")...)
	if code != "" {
		c.publishError(code, now)
		return
	}
	save, verified := vnStatUTCConfig(config)
	if !verified {
		c.publishError(traffic.UnsupportedTimebase, now)
		return
	}
	raw, code := c.run(ctx, path, append(args[:len(args):len(args)], "--json", "d", "--limit", "0")...)
	if code != "" {
		c.publishError(code, now)
		return
	}
	snapshot, code := parseVNStatDaily(raw, now, c.cfg.validFrom)
	if code != "" {
		c.publishError(code, now)
		return
	}
	snapshot.RefreshSeconds = int64(c.cfg.refresh / time.Second)
	snapshot.SaveIntervalSeconds = save
	if err := snapshot.Validate(now); err != nil {
		c.publishError(traffic.InvalidJSON, now)
		return
	}
	c.current.Store(snapshot)
}

func vnStatUTCConfig(raw []byte) (saveSeconds int64, valid bool) {
	var utc string
	for line := range strings.SplitSeq(string(raw), "\n") {
		// --showconfig prints defaults as e.g. ;UseUTC 0. This is output
		// parsing, not treating commented values in a config file as active.
		parts := strings.Fields(strings.TrimLeft(line, "#; \t"))
		if len(parts) < 2 {
			continue
		}
		if parts[0] == "UseUTC" {
			utc = parts[1]
		}
		if parts[0] == "SaveInterval" {
			minutes, err := strconv.ParseInt(parts[1], 10, 64)
			if err == nil && minutes >= 1 && minutes <= 60 {
				saveSeconds = minutes * 60
			}
		}
	}
	return saveSeconds, utc == "1" && saveSeconds > 0
}

type limitedVNStatOutput struct {
	buffer   bytes.Buffer // Do not embed: Buffer.ReadFrom would bypass Write's limit.
	limit    int
	cancel   context.CancelFunc
	exceeded bool
}

func (w *limitedVNStatOutput) Write(p []byte) (int, error) {
	if len(p) > w.limit-w.buffer.Len() {
		w.exceeded = true
		w.cancel()
		return 0, errors.New("vnStat output limit")
	}
	return w.buffer.Write(p)
}

// CommandContext and WaitDelay bound both the process and inherited output pipes.
// https://pkg.go.dev/os/exec#Cmd.WaitDelay. No shell or user-supplied argv.
func runVNStatCommand(parent context.Context, path string, args ...string) ([]byte, string) {
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	out := &limitedVNStatOutput{limit: traffic.MaxPayload, cancel: cancel}
	errout := &limitedVNStatOutput{limit: 8 * 1024, cancel: cancel}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Stdout, cmd.Stderr, cmd.WaitDelay = out, errout, 100*time.Millisecond
	err := cmd.Run()
	if out.exceeded || errout.exceeded {
		return nil, traffic.TooLarge
	}
	if ctx.Err() != nil {
		return nil, traffic.Timeout
	}
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, exec.ErrNotFound) {
		return nil, traffic.Missing
	}
	if errors.Is(err, os.ErrPermission) {
		return nil, traffic.PermissionDenied
	}
	if err != nil {
		// Never log/forward raw stderr. Only map known diagnostics to codes.
		message := strings.ToLower(out.buffer.String() + errout.buffer.String())
		switch {
		case strings.Contains(message, "permission denied"):
			return nil, traffic.PermissionDenied
		case strings.Contains(message, "no interfaces"):
			return nil, traffic.NoInterfaces
		case strings.Contains(message, "no database") || strings.Contains(message, "database not found"):
			return nil, traffic.NoDatabase
		default:
			return nil, traffic.ExecFailed
		}
	}
	return out.buffer.Bytes(), ""
}

type vnStatDate struct{ Year, Month, Day int }
type vnStatDay struct {
	Date      *vnStatDate
	Timestamp *int64
	Rx, Tx    *uint64
}
type vnStatInterface struct {
	Name             string
	Created, Updated struct{ Timestamp int64 }
	Traffic          struct{ Day *[]vnStatDay }
}

func parseVNStatDaily(raw []byte, now time.Time, validFrom string) (*traffic.Snapshot, string) {
	if len(raw) > traffic.MaxPayload {
		return nil, traffic.TooLarge
	}
	var input struct {
		VNStatVersion string `json:"vnstatversion"`
		JSONVersion   string `json:"jsonversion"`
		Interfaces    *[]vnStatInterface
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(&input); err != nil {
		return nil, traffic.InvalidJSON
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, traffic.InvalidJSON
	}
	if input.JSONVersion != "2" {
		return nil, traffic.UnsupportedVersion
	}
	switch input.VNStatVersion {
	case "2.10", "2.12", "2.13":
	default:
		return nil, traffic.UnsupportedVersion
	}
	if input.Interfaces == nil {
		return nil, traffic.InvalidJSON
	}
	if len(*input.Interfaces) == 0 {
		return nil, traffic.NoInterfaces
	}
	if len(*input.Interfaces) > traffic.MaxInterfaces {
		return nil, traffic.TooLarge
	}
	from, err := traffic.ParseDay(validFrom)
	if err != nil || from.After(now) {
		return nil, traffic.UnsupportedTimebase
	}
	snapshot := &traffic.Snapshot{SchemaVersion: traffic.SchemaVersion, Status: traffic.OK, ObservedAt: now.Unix(), Timebase: "utc", UTCValidFromDay: validFrom, VNStatVersion: input.VNStatVersion}
	for _, source := range *input.Interfaces {
		if source.Traffic.Day == nil {
			return nil, traffic.InvalidJSON
		}
		if len(*source.Traffic.Day) > traffic.MaxDays {
			return nil, traffic.TooLarge
		}
		iface := traffic.InterfaceDaily{Name: source.Name, SourceCreatedAt: source.Created.Timestamp, SourceUpdatedAt: source.Updated.Timestamp, Days: []traffic.DailyBucket{}}
		for _, value := range *source.Traffic.Day {
			if value.Date == nil || value.Rx == nil || value.Tx == nil || value.Timestamp == nil {
				return nil, traffic.InvalidJSON
			}
			day := fmt.Sprintf("%04d-%02d-%02d", value.Date.Year, value.Date.Month, value.Date.Day)
			date, err := traffic.ParseDay(day)
			if err != nil {
				return nil, traffic.InvalidJSON
			}
			if date.Before(from) {
				continue
			} // Never reinterpret unverified historic dates.
			if date.Unix() != *value.Timestamp {
				return nil, traffic.UnsupportedTimebase
			}
			iface.Days = append(iface.Days, traffic.DailyBucket{Day: day, RxBytes: *value.Rx, TxBytes: *value.Tx})
		}
		snapshot.Interfaces = append(snapshot.Interfaces, iface)
	}
	return snapshot, ""
}
