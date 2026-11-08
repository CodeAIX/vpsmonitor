package agent

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/CodeAIX/vpsmonitor/internal/common"
	"github.com/CodeAIX/vpsmonitor/internal/entities/system"
	"github.com/CodeAIX/vpsmonitor/internal/history"
	"github.com/fxamacker/cbor/v2"
)

type agentHistory struct {
	store *history.Store
	queue chan []byte
}

func newAgentHistory(dir string) *agentHistory {
	if os.Getenv("VPSMONITOR_HISTORY_ENABLED") == "false" || dir == "" {
		return nil
	}
	budget := history.DefaultMaxBytes
	if raw := os.Getenv("VPSMONITOR_HISTORY_MAX_BYTES"); raw != "" {
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || v < 8*1024*1024 || v > 1024*1024*1024 {
			slog.Warn("Invalid VPSMONITOR_HISTORY_MAX_BYTES; using 256 MiB")
		} else {
			budget = v
		}
	}
	s, err := history.Open(filepath.Join(dir, "history-v1"), budget, time.Now())
	if err != nil {
		slog.Warn("Agent history unavailable; live monitoring continues")
		return &agentHistory{}
	}
	return &agentHistory{store: s, queue: make(chan []byte, 2)}
}

// Called while gatherStats owns the Agent lock. Freeze nested maps/slices into
// bounded bytes before the next collector mutation, then hand off disk work.
func (h *agentHistory) enqueue(data *system.CombinedData) {
	if h.store == nil {
		return
	}
	encoded, err := json.Marshal(system.HistorySample{At: time.Now().UnixMilli(), Stats: data.Stats, Containers: data.Containers})
	if err != nil || len(encoded) > system.HistoryMaxSampleBytes {
		h.store.SetError("sample_too_large")
		return
	}
	select {
	case h.queue <- encoded:
	default:
		h.store.SetError("writer_busy")
	}
}

func (h *agentHistory) info() system.HistoryInfo {
	if h.store == nil {
		return system.HistoryInfo{Version: system.HistoryVersion, RetentionSeconds: int64(system.HistoryRetention / time.Second), Error: "storage_error"}
	}
	return h.store.Info()
}

func (h *agentHistory) start(ctx context.Context, a *Agent) func() {
	if h.store == nil {
		return func() {}
	}
	ctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Go(func() {
		cleanup := time.NewTicker(time.Hour)
		defer cleanup.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-cleanup.C:
				h.store.Prune(now)
			case b := <-h.queue:
				var sample system.HistorySample
				if json.Unmarshal(b, &sample) == nil {
					if err := h.store.Append(sample, time.Now()); err != nil {
						slog.Debug("History sample not persisted", "err", err)
					}
				}
			}
		}
	})
	wg.Go(func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			if ctx.Err() != nil {
				return
			}
			a.gatherStatsInternal(common.DataRequestOptions{CacheTimeMs: defaultDataCacheTimeMs}, true)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	})
	return func() { cancel(); wg.Wait() }
}

type GetHistoryHandler struct{}

func (*GetHistoryHandler) Handle(ctx *HandlerContext) error {
	var req system.HistoryRequest
	if err := cbor.Unmarshal(ctx.Request.Data, &req); err != nil {
		return err
	}
	if ctx.Agent.history == nil {
		return ctx.SendResponse(system.HistoryPage{}, ctx.RequestID)
	}
	if ctx.Agent.history.store == nil {
		return ctx.SendResponse(system.HistoryPage{Version: system.HistoryVersion, Next: req.After, Info: ctx.Agent.history.info()}, ctx.RequestID)
	}
	now := time.Now()
	for {
		page, err := ctx.Agent.history.store.Page(req, now)
		if err != nil {
			return err
		}
		count := len(page.Samples)
		if req.Daily {
			if err := page.PackDaily(); err != nil {
				return err
			}
		}
		encoded, err := cbor.Marshal(page)
		if err != nil {
			return err
		}
		if len(encoded) <= system.HistoryMaxPageBytes {
			return ctx.SendResponse(page, ctx.RequestID)
		}
		if !req.Daily || count < 2 {
			return errors.New("history page exceeds limit")
		}
		// Incompressible days may need more than one bounded message.
		req.Limit = count / 2
	}
}
