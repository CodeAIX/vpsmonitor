package system

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/CodeAIX/vpsmonitor/internal/entities/container"
)

const (
	HistoryVersion          = 1
	HistoryLegacyRetention  = 7 * 24 * time.Hour
	HistoryRetention        = 31 * 24 * time.Hour
	HistoryMaxSampleBytes   = 256 * 1024
	HistoryMaxPageBytes     = 1024 * 1024
	HistoryMaxPageSamples   = 128
	HistoryDailySamples     = 24 * 60
	HistoryMaxDailyRawBytes = 4 * HistoryMaxPageBytes
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
	DailyPages       bool   `json:"daily_pages,omitempty" cbor:"8,keyasint,omitempty"`
}

type HistoryRequest struct {
	After  int64 `cbor:"0,keyasint"`
	Before int64 `cbor:"1,keyasint"`
	Limit  int   `cbor:"2,keyasint"`
	Daily  bool  `cbor:"3,keyasint,omitempty"`
}

func (r HistoryRequest) MaxSamples() int {
	if r.Daily {
		return HistoryDailySamples
	}
	return HistoryMaxPageSamples
}

type HistoryPage struct {
	Version uint8           `cbor:"0,keyasint"`
	Samples []HistorySample `cbor:"1,keyasint"`
	Next    int64           `cbor:"2,keyasint"`
	More    bool            `cbor:"3,keyasint"`
	Info    HistoryInfo     `cbor:"4,keyasint"`
	Packed  []byte          `cbor:"5,keyasint,omitempty" json:"-"`
}

// PackDaily keeps the existing envelope and batches minute samples only when
// the Hub requests daily pages.
func (p *HistoryPage) PackDaily() error {
	if len(p.Samples) == 0 {
		return nil
	}
	raw, err := json.Marshal(p.Samples)
	if err != nil || len(raw) > HistoryMaxDailyRawBytes {
		return errors.New("daily history page exceeds decoded limit")
	}
	var packed bytes.Buffer
	z := gzip.NewWriter(&packed)
	if _, err := z.Write(raw); err != nil {
		return err
	}
	if err := z.Close(); err != nil {
		return err
	}
	p.Packed, p.Samples = packed.Bytes(), nil
	return nil
}

func (p *HistoryPage) UnpackDaily(req HistoryRequest) error {
	if len(p.Packed) == 0 {
		return nil
	}
	if !req.Daily || req.Limit < 1 || req.Limit > req.MaxSamples() || len(p.Samples) != 0 || len(p.Packed) > HistoryMaxPageBytes {
		return errors.New("unexpected packed history page")
	}
	z, err := gzip.NewReader(bytes.NewReader(p.Packed))
	if err != nil {
		return err
	}
	defer z.Close()
	raw, err := io.ReadAll(io.LimitReader(z, HistoryMaxDailyRawBytes+1))
	if err != nil || len(raw) > HistoryMaxDailyRawBytes {
		return errors.New("daily history page exceeds decoded limit")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	token, err := dec.Token()
	if err != nil || token != json.Delim('[') {
		return errors.New("invalid daily history sample list")
	}
	// Decode incrementally so a tiny payload containing excessive empty
	// samples cannot allocate a large slice of sample structs.
	samples := make([]HistorySample, 0, req.Limit)
	for dec.More() {
		if len(samples) == req.Limit {
			return errors.New("too many packed history samples")
		}
		var sample HistorySample
		if err := dec.Decode(&sample); err != nil {
			return err
		}
		samples = append(samples, sample)
	}
	if _, err := dec.Token(); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return errors.New("trailing daily history data")
	}
	p.Samples, p.Packed = samples, nil
	return nil
}
