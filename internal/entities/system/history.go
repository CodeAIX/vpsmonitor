package system

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/CodeAIX/vpsmonitor/internal/entities/container"
)

const (
	HistoryVersion        = 1
	HistoryRetention      = 7 * 24 * time.Hour
	HistoryMaxSampleBytes = 256 * 1024
	HistoryMaxPageBytes   = 1024 * 1024
	HistoryMaxPageSamples = 128
)

// HistorySample deliberately excludes Info, details, services, alerts and
// traffic. Replay must never overwrite current state or run present-day alerts.
type HistorySample struct {
	At         int64              `json:"at" cbor:"0,keyasint"`
	Stats      Stats              `json:"stats" cbor:"1,keyasint"`
	Containers []*container.Stats `json:"containers" cbor:"2,keyasint"`
}

func (s *HistorySample) Validate(now time.Time) error {
	if s.At <= 0 || s.At > now.Add(5*time.Minute).UnixMilli() || s.At < now.Add(-HistoryRetention-time.Hour).UnixMilli() || len(s.Containers) > 4096 {
		return errors.New("invalid history sample time or container count")
	}
	for _, c := range s.Containers {
		if c == nil {
			return errors.New("nil history container")
		}
	}
	b, err := json.Marshal(s)
	if err != nil || len(b) > HistoryMaxSampleBytes {
		return errors.New("invalid or oversized history sample")
	}
	return nil
}

type HistoryInfo struct {
	Version          uint8  `json:"version" cbor:"0,keyasint"`
	RetentionSeconds int64  `json:"retention_seconds" cbor:"1,keyasint"`
	Oldest           int64  `json:"oldest" cbor:"2,keyasint"`
	Newest           int64  `json:"newest" cbor:"3,keyasint"`
	Samples          int    `json:"samples" cbor:"4,keyasint"`
	Bytes            int64  `json:"bytes" cbor:"5,keyasint"`
	Error            string `json:"error,omitempty" cbor:"6,keyasint,omitempty"`
	Limited          bool   `json:"limited" cbor:"7,keyasint"`
}

type HistoryRequest struct {
	After  int64 `cbor:"0,keyasint"`
	Before int64 `cbor:"1,keyasint"`
	Limit  int   `cbor:"2,keyasint"`
}

type HistoryPage struct {
	Version uint8           `cbor:"0,keyasint"`
	Samples []HistorySample `cbor:"1,keyasint"`
	Next    int64           `cbor:"2,keyasint"`
	More    bool            `cbor:"3,keyasint"`
	Info    HistoryInfo     `cbor:"4,keyasint"`
}
