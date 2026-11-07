// Package history owns the bounded Agent journal. It is independent of Hub
// acknowledgement: two Hubs never delete each other's not-yet-synced samples.
package history

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/CodeAIX/vpsmonitor/internal/entities/system"
)

const DefaultMaxBytes int64 = 256 * 1024 * 1024

type entry struct {
	at, size int64
	path     string
}
type Store struct {
	mu           sync.RWMutex
	writeMu      sync.Mutex
	dir          string
	maxBytes     int64
	entries      []entry
	info         system.HistoryInfo
	limitedUntil int64
}

func Open(dir string, maxBytes int64, now time.Time) (*Store, error) {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	files, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	s := &Store{dir: dir, maxBytes: maxBytes, info: system.HistoryInfo{Version: system.HistoryVersion, RetentionSeconds: int64(system.HistoryRetention / time.Second)}}
	if f, err := os.Open(filepath.Join(dir, ".metadata.json")); err == nil {
		var meta struct {
			LimitedUntil int64 `json:"limited_until"`
		}
		b, readErr := io.ReadAll(io.LimitReader(f, 513))
		_ = f.Close()
		if readErr == nil && len(b) <= 512 && json.Unmarshal(b, &meta) == nil && meta.LimitedUntil >= 0 && meta.LimitedUntil <= now.Add(system.HistoryRetention+5*time.Minute).UnixMilli() {
			s.limitedUntil = meta.LimitedUntil
		} else {
			s.info.Error = "corrupt_metadata"
		}
	}
	for _, f := range files {
		if !strings.HasSuffix(f.Name(), ".json.gz") || f.Type()&os.ModeSymlink != 0 {
			continue
		}
		at, err := strconv.ParseInt(strings.TrimSuffix(f.Name(), ".json.gz"), 10, 64)
		if err != nil || at <= 0 {
			continue
		}
		path := filepath.Join(dir, f.Name())
		stat, err := f.Info()
		if err != nil || !stat.Mode().IsRegular() {
			continue
		}
		if at > now.Add(5*time.Minute).UnixMilli() || stat.Size() > system.HistoryMaxSampleBytes+1024 {
			s.info.Error = "corrupt_sample"
			_ = os.Remove(path)
			continue
		}
		s.entries = append(s.entries, entry{at: at, size: stat.Size(), path: path})
		s.info.Bytes += stat.Size()
	}
	sort.Slice(s.entries, func(i, j int) bool { return s.entries[i].at < s.entries[j].at })
	s.prune(now)
	return s, nil
}

func (s *Store) refreshLocked() {
	s.info.Samples = len(s.entries)
	s.info.Oldest = 0
	s.info.Newest = 0
	if len(s.entries) > 0 {
		s.info.Oldest = s.entries[0].at
		s.info.Newest = s.entries[len(s.entries)-1].at
	}
}

func (s *Store) Info() system.HistoryInfo { s.mu.RLock(); defer s.mu.RUnlock(); return s.info }
func (s *Store) SetError(code string)     { s.mu.Lock(); s.info.Error = code; s.mu.Unlock() }

// prune changes the index under a short lock; filesystem operations never hold
// the metadata lock used by GetData's normal monitoring response.
func (s *Store) prune(now time.Time) {
	s.mu.Lock()
	var deleted []entry
	previousUntil := s.limitedUntil
	for len(s.entries) > 0 {
		e := s.entries[0]
		if e.at >= now.Add(-system.HistoryRetention).UnixMilli() && s.info.Bytes <= s.maxBytes {
			break
		}
		if e.at >= now.Add(-system.HistoryRetention).UnixMilli() {
			s.limitedUntil = max(s.limitedUntil, e.at+system.HistoryRetention.Milliseconds())
		}
		deleted = append(deleted, e)
		s.entries = s.entries[1:]
		s.info.Bytes -= e.size
	}
	if s.limitedUntil <= now.UnixMilli() {
		s.limitedUntil = 0
	}
	s.info.Limited = s.limitedUntil > 0
	until := s.limitedUntil
	s.refreshLocked()
	s.mu.Unlock()
	for _, e := range deleted {
		if err := os.Remove(e.path); err != nil && !os.IsNotExist(err) {
			s.SetError("storage_error")
		}
	}
	if previousUntil != until {
		b, _ := json.Marshal(struct {
			LimitedUntil int64 `json:"limited_until"`
		}{until})
		if err := writeMetadata(s.dir, b); err != nil {
			s.SetError("storage_error")
		}
	}
}

func writeMetadata(dir string, b []byte) error {
	f, err := os.CreateTemp(dir, ".meta-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), filepath.Join(dir, ".metadata.json"))
}

func (s *Store) Append(sample system.HistorySample, now time.Time) error {
	if err := sample.Validate(now); err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	info := s.Info()
	if sample.At/60000 <= info.Newest/60000 && info.Newest > 0 {
		if sample.At < info.Newest {
			s.SetError("clock_regression")
		}
		return nil
	}
	raw, err := json.Marshal(sample)
	if err != nil {
		return err
	}
	var compressed bytes.Buffer
	z := gzip.NewWriter(&compressed)
	if _, err = z.Write(raw); err != nil {
		return err
	}
	if err = z.Close(); err != nil {
		return err
	}
	f, err := os.CreateTemp(s.dir, ".sample-*")
	if err != nil {
		s.SetError("storage_error")
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(compressed.Bytes())
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	path := filepath.Join(s.dir, fmt.Sprintf("%013d.json.gz", sample.At))
	if err == nil {
		err = os.Rename(f.Name(), path)
	}
	if err != nil {
		s.SetError("storage_error")
		return err
	}
	// Make the renamed entry durable on filesystems supporting directory sync.
	if d, err := os.Open(s.dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	s.mu.Lock()
	s.entries = append(s.entries, entry{at: sample.At, size: int64(compressed.Len()), path: path})
	s.info.Bytes += int64(compressed.Len())
	s.info.Error = ""
	s.mu.Unlock()
	s.prune(now)
	return nil
}

func readSample(e entry, now time.Time) (system.HistorySample, int, error) {
	var sample system.HistorySample
	f, err := os.Open(e.path)
	if err != nil {
		return sample, 0, err
	}
	defer f.Close()
	z, err := gzip.NewReader(io.LimitReader(f, system.HistoryMaxSampleBytes+1024))
	if err != nil {
		return sample, 0, err
	}
	defer z.Close()
	b, err := io.ReadAll(io.LimitReader(z, system.HistoryMaxSampleBytes+1))
	if err != nil || len(b) > system.HistoryMaxSampleBytes {
		return sample, 0, errors.New("oversized journal sample")
	}
	if err = json.Unmarshal(b, &sample); err != nil {
		return sample, 0, err
	}
	if sample.At != e.at {
		return sample, 0, errors.New("journal timestamp mismatch")
	}
	return sample, len(b), sample.Validate(now)
}

func (s *Store) Page(req system.HistoryRequest, now time.Time) (system.HistoryPage, error) {
	page := system.HistoryPage{Version: system.HistoryVersion, Next: req.After, Samples: []system.HistorySample{}}
	if req.After < 0 || req.Before < req.After || req.Limit < 1 || req.Limit > system.HistoryMaxPageSamples {
		return page, errors.New("invalid history page request")
	}
	s.mu.RLock()
	entries := append([]entry(nil), s.entries...)
	s.mu.RUnlock()
	used := 0
	for i, e := range entries {
		if e.at <= req.After || e.at > req.Before {
			continue
		}
		if len(page.Samples) >= req.Limit {
			page.More = true
			break
		}
		v, size, err := readSample(e, now)
		if err != nil {
			s.SetError("corrupt_sample")
			page.Next = e.at
			continue
		}
		if used+size > system.HistoryMaxPageBytes/2 && len(page.Samples) > 0 {
			page.More = true
			break
		}
		page.Samples = append(page.Samples, v)
		used += size
		page.Next = e.at
		if i+1 == len(entries) {
			page.More = false
		}
	}
	page.Info = s.Info()
	return page, nil
}
