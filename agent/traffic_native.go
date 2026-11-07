package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync/atomic"
	"time"

	"github.com/CodeAIX/vpsmonitor"
	"github.com/CodeAIX/vpsmonitor/internal/accounting"
	"github.com/CodeAIX/vpsmonitor/internal/entities/traffic"
)

type nativeTrafficCollector struct {
	dir     string
	current atomic.Pointer[traffic.Snapshot]
}

func newTrafficCollector(dir string) *nativeTrafficCollector {
	if os.Getenv("VPSMONITOR_TRAFFIC_ENABLED") == "false" {
		return nil
	}
	c := &nativeTrafficCollector{dir: dir}
	c.publishError(traffic.Initializing, time.Now())
	return c
}

func (c *nativeTrafficCollector) publishError(code string, now time.Time) {
	c.current.Store(&traffic.Snapshot{SchemaVersion: traffic.SchemaVersion, Status: code, ErrorCode: code, ObservedAt: now.Unix(), Timebase: "utc", RefreshSeconds: 30})
}

func (c *nativeTrafficCollector) start(parent context.Context) func() {
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	go func() {
		defer close(done)
		var ledger *accounting.Ledger
		defer func() {
			if ledger != nil {
				_ = ledger.Close()
			}
		}()
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			now := time.Now()
			r, code := readNativeTraffic()
			if code != "" {
				c.publishError(code, now)
			} else {
				if ledger == nil {
					sum := sha256.Sum256([]byte(r.Name))
					var err error
					ledger, err = accounting.Open(filepath.Join(c.dir, "traffic", hex.EncodeToString(sum[:8])))
					if err != nil {
						slog.Warn("Native traffic storage unavailable; live monitoring continues")
						c.publishError(traffic.StorageError, now)
					}
				}
				if ledger != nil {
					state, err := ledger.Record(r, now)
					if err != nil {
						code := traffic.StorageError
						if errors.Is(err, accounting.ErrClockBackwards) {
							code = traffic.ClockError
						}
						if errors.Is(err, accounting.ErrInterfaceChanged) {
							code = traffic.InterfaceRequired
						}
						c.publishError(code, now)
					} else {
						c.current.Store(nativeSnapshot(state, now))
					}
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return func() { cancel(); <-done }
}

func nativeSnapshot(s accounting.State, now time.Time) *traffic.Snapshot {
	iface := traffic.InterfaceDaily{Name: s.Baseline.Name, SourceID: s.ID, SourceCreatedAt: s.Created, SourceUpdatedAt: s.Updated, Days: []traffic.DailyBucket{}}
	keys := make([]string, 0, len(s.Days))
	for key := range s.Days {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		d := s.Days[key]
		iface.Days = append(iface.Days, traffic.DailyBucket{Day: key, RxBytes: d.RX, TxBytes: d.TX, Partial: d.Partial})
	}
	return &traffic.Snapshot{SchemaVersion: traffic.SchemaVersion, Status: traffic.OK, ObservedAt: now.Unix(), Timebase: "utc", Interfaces: []traffic.InterfaceDaily{iface}, RefreshSeconds: 30, SaveIntervalSeconds: 30, UTCValidFromDay: time.Unix(s.Created, 0).UTC().Format(time.DateOnly), CollectorVersion: vpsmonitor.BuildVersion}
}
