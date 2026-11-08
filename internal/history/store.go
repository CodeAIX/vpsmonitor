// Package history owns the bounded Agent journal. Reading it never consumes
// samples, so reconnecting Hubs can each advance their own cursor.
package history

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/CodeAIX/vpsmonitor/internal/entities/system"
)

const (
	DefaultMaxBytes int64 = 256 * 1024 * 1024
	frameHeader           = 12 // Unix milliseconds (8 bytes), gzip length (4 bytes).
	maxCompressed         = system.HistoryMaxSampleBytes + 1024
	dayMillis             = int64(24 * time.Hour / time.Millisecond)
	maxSegmentBytes       = 4 * 1024 * 1024
)

type segment struct {
	path        string
	first, last int64
	size        int64
	legacy      bool
	sealed      bool
}

type entry struct {
	*segment
	at, offset int64
	length     int
}

type Store struct {
	mu           sync.RWMutex // Published diagnostics, independent of disk I/O.
	filesMu      sync.RWMutex // Prevents removal while a page is reading.
	dir          string
	maxBytes     int64
	diskBytes    int64
	files        []*segment
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
	s := &Store{dir: dir, maxBytes: maxBytes, info: system.HistoryInfo{
		Version: system.HistoryVersion, RetentionSeconds: int64(system.HistoryRetention / time.Second), DailyPages: true,
	}}
	if f, err := os.Open(filepath.Join(dir, ".metadata.json")); err == nil {
		b, readErr := io.ReadAll(io.LimitReader(f, 513))
		_ = f.Close()
		var meta struct {
			LimitedUntil int64 `json:"limited_until"`
		}
		if readErr == nil && len(b) <= 512 && json.Unmarshal(b, &meta) == nil && meta.LimitedUntil >= 0 && meta.LimitedUntil <= now.Add(system.HistoryRetention+5*time.Minute).UnixMilli() {
			s.limitedUntil = meta.LimitedUntil
		} else {
			s.SetError("corrupt_metadata")
		}
	}
	for _, f := range files {
		name := f.Name()
		if f.Type()&os.ModeSymlink != 0 {
			continue
		}
		// These are private temp files from interrupted legacy writes or metadata commits.
		if strings.HasPrefix(name, ".sample-") || strings.HasPrefix(name, ".meta-") {
			if f.Type().IsRegular() {
				if err := os.Remove(filepath.Join(dir, name)); err != nil {
					s.SetError("storage_error")
				}
			}
			continue
		}
		legacy := strings.HasSuffix(name, ".json.gz")
		suffix := ".daylog"
		if legacy {
			suffix = ".json.gz"
		}
		stamp, ok := strings.CutSuffix(name, suffix)
		at, err := strconv.ParseInt(stamp, 10, 64)
		if !ok || err != nil || at <= 0 {
			continue
		}
		stat, err := f.Info()
		if err != nil || !stat.Mode().IsRegular() {
			continue
		}
		part := &segment{path: filepath.Join(dir, name), first: at, last: at, size: stat.Size(), legacy: legacy}
		switch {
		case at > now.Add(5*time.Minute).UnixMilli(), legacy && part.size > maxCompressed:
			s.SetError("corrupt_sample")
			if err := os.Remove(part.path); err == nil || os.IsNotExist(err) {
				continue
			}
			part.sealed = true
			s.SetError("storage_error")
		case legacy:
			s.entries = append(s.entries, entry{segment: part, at: at, length: int(part.size)})
		default:
			if err := s.scanSegment(part, now); err != nil {
				// Preserve the readable prefix and never append past a bad header.
				part.sealed = true
				s.SetError("corrupt_sample")
			} else if part.size == 0 {
				if err := os.Remove(part.path); err == nil || os.IsNotExist(err) {
					continue
				}
				part.sealed = true
				s.SetError("storage_error")
			}
		}
		s.files = append(s.files, part)
		s.diskBytes += part.size
	}
	sort.Slice(s.files, func(i, j int) bool { return s.files[i].last < s.files[j].last })
	sort.Slice(s.entries, func(i, j int) bool {
		a, b := s.entries[i], s.entries[j]
		// After a rollback, beta.3 may have written a legacy file for a minute
		// already archived by this Agent. Prefer the packed copy.
		if a.at/60000 == b.at/60000 && a.legacy != b.legacy {
			return !a.legacy
		}
		return a.at < b.at
	})
	s.entries = slices.CompactFunc(s.entries, func(a, b entry) bool { return a.at/60000 == b.at/60000 })
	s.prune(now, 0)
	return s, nil
}

// A frame is appended and fsynced once per minute. A restart can truncate a
// partial final frame without rewriting or decompressing the rest of the day.
func (s *Store) scanSegment(part *segment, now time.Time) error {
	f, err := os.OpenFile(part.path, os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	var header [frameHeader]byte
	for offset := int64(0); offset < part.size; {
		if _, err := f.ReadAt(header[:], offset); err != nil {
			if errors.Is(err, io.EOF) {
				return truncateTail(f, part, offset)
			}
			return err
		}
		at := int64(binary.BigEndian.Uint64(header[:8]))
		length := int(binary.BigEndian.Uint32(header[8:]))
		if length <= 0 || length > maxCompressed || at/dayMillis != part.first/dayMillis ||
			at > now.Add(5*time.Minute).UnixMilli() ||
			(offset == 0 && at != part.first) || (offset > 0 && at/60000 <= part.last/60000) {
			return errors.New("invalid daily journal frame")
		}
		if offset+frameHeader+int64(length) > part.size {
			return truncateTail(f, part, offset)
		}
		s.entries = append(s.entries, entry{segment: part, at: at, offset: offset + frameHeader, length: length})
		part.last = at
		offset += frameHeader + int64(length)
	}
	return nil
}

func truncateTail(f *os.File, part *segment, offset int64) error {
	if err := f.Truncate(offset); err != nil {
		return err
	}
	part.size = offset
	return f.Sync()
}

func (s *Store) Info() system.HistoryInfo { s.mu.RLock(); defer s.mu.RUnlock(); return s.info }
func (s *Store) SetError(code string)     { s.mu.Lock(); s.info.Error = code; s.mu.Unlock() }

// Prune reclaims expired files even when no valid samples are being written.
func (s *Store) Prune(now time.Time) {
	s.filesMu.Lock()
	defer s.filesMu.Unlock()
	s.prune(now, 0)
}

func (s *Store) prune(now time.Time, reserve int64) {
	cutoff := now.Add(-system.HistoryRetention).UnixMilli()
	previousUntil := s.limitedUntil
	var deleted map[*segment]bool
	s.files = slices.DeleteFunc(s.files, func(part *segment) bool {
		if part.last >= cutoff && s.diskBytes+reserve <= s.maxBytes {
			return false
		}
		if err := os.Remove(part.path); err != nil && !os.IsNotExist(err) {
			s.SetError("storage_error")
			return false // Keep counting its bytes, and retry on the next sample.
		}
		if part.last >= cutoff {
			s.limitedUntil = max(s.limitedUntil, part.last+system.HistoryRetention.Milliseconds())
		}
		if deleted == nil {
			deleted = make(map[*segment]bool)
		}
		deleted[part] = true
		s.diskBytes -= part.size
		return true
	})
	if len(deleted) != 0 {
		s.entries = slices.DeleteFunc(s.entries, func(e entry) bool { return deleted[e.segment] })
		s.syncDir()
	}
	if s.limitedUntil <= now.UnixMilli() {
		s.limitedUntil = 0
	}
	if previousUntil != s.limitedUntil {
		b, _ := json.Marshal(struct {
			LimitedUntil int64 `json:"limited_until"`
		}{s.limitedUntil})
		if err := s.writeMetadata(b); err != nil {
			s.SetError("storage_error")
		}
	}
	s.refresh(cutoff)
}

func (s *Store) refresh(cutoff int64) {
	first := sort.Search(len(s.entries), func(i int) bool { return s.entries[i].at >= cutoff })
	s.mu.Lock()
	defer s.mu.Unlock()
	s.info.Bytes = s.diskBytes
	s.info.Limited = s.limitedUntil != 0
	s.info.Samples = len(s.entries) - first
	s.info.Oldest, s.info.Newest = 0, 0
	if first < len(s.entries) {
		s.info.Oldest = s.entries[first].at
		s.info.Newest = s.entries[len(s.entries)-1].at
	}
}

func (s *Store) syncDir() {
	if d, err := os.Open(s.dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
}

func (s *Store) writeMetadata(b []byte) error {
	f, err := os.CreateTemp(s.dir, ".meta-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := os.Rename(f.Name(), filepath.Join(s.dir, ".metadata.json")); err != nil {
		return err
	}
	s.syncDir()
	return nil
}

func (s *Store) Append(sample system.HistorySample, now time.Time) error {
	if err := sample.Validate(now); err != nil {
		return err
	}
	s.filesMu.Lock()
	defer s.filesMu.Unlock()
	if len(s.entries) > 0 {
		newest := s.entries[len(s.entries)-1].at
		if sample.At/60000 <= newest/60000 {
			if sample.At < newest {
				s.SetError("clock_regression")
			}
			return nil
		}
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
	size := int64(frameHeader + compressed.Len())
	if size > s.maxBytes {
		s.SetError("storage_error")
		return errors.New("sample exceeds journal capacity")
	}
	s.SetError("")
	s.prune(now, size)
	if s.diskBytes+size > s.maxBytes {
		return errors.New("could not reclaim journal capacity")
	}
	if err := s.appendFrame(sample.At, compressed.Bytes(), size); err != nil {
		s.refresh(now.Add(-system.HistoryRetention).UnixMilli())
		s.SetError("storage_error")
		return err
	}
	s.refresh(now.Add(-system.HistoryRetention).UnixMilli())
	return nil
}

func (s *Store) appendFrame(at int64, payload []byte, size int64) error {
	var part *segment
	if n := len(s.entries); n > 0 {
		part = s.entries[n-1].segment
	}
	// A typical day is one file. Cap individual files so a busy day cannot
	// consume the journal or force whole-day eviction under capacity pressure.
	newFile := part == nil || part.legacy || part.sealed || part.first/dayMillis != at/dayMillis ||
		part.size+size > min(maxSegmentBytes, s.maxBytes/4)
	flags := os.O_WRONLY | os.O_APPEND
	if newFile {
		part = &segment{path: filepath.Join(s.dir, fmt.Sprintf("%013d.daylog", at)), first: at}
		flags |= os.O_CREATE | os.O_EXCL
	}
	f, err := os.OpenFile(part.path, flags, 0600)
	if err != nil {
		return err
	}
	frame := make([]byte, frameHeader, int(size))
	binary.BigEndian.PutUint64(frame[:8], uint64(at))
	binary.BigEndian.PutUint32(frame[8:], uint32(len(payload)))
	frame = append(frame, payload...)
	if _, err = f.Write(frame); err == nil {
		err = f.Sync()
	}
	if err != nil {
		if truncateErr := f.Truncate(part.size); truncateErr != nil {
			part.sealed = true
			// Account for a failed write even if the damaged suffix cannot be removed.
			if stat, statErr := f.Stat(); statErr == nil && stat.Size() > part.size {
				s.diskBytes += stat.Size() - part.size
				part.size = stat.Size()
			}
			if newFile {
				part.last = at
				s.files = append(s.files, part)
			}
		} else if newFile {
			_ = os.Remove(part.path)
		}
		_ = f.Close()
		return err
	}
	_ = f.Close()
	if newFile {
		s.files = append(s.files, part)
		s.syncDir()
	}
	s.entries = append(s.entries, entry{segment: part, at: at, offset: part.size + frameHeader, length: len(payload)})
	part.size += size
	part.last = at
	s.diskBytes += size
	return nil
}

func readSample(f *os.File, e entry, now time.Time) (system.HistorySample, int, error) {
	var sample system.HistorySample
	z, err := gzip.NewReader(io.NewSectionReader(f, e.offset, int64(e.length)))
	if err != nil {
		return sample, 0, err
	}
	defer z.Close()
	b, err := io.ReadAll(io.LimitReader(z, system.HistoryMaxSampleBytes+1))
	if err != nil || len(b) > system.HistoryMaxSampleBytes {
		return sample, 0, errors.New("oversized or corrupt journal sample")
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
	if req.After < 0 || req.Before < req.After || req.Limit < 1 || req.Limit > req.MaxSamples() {
		return page, errors.New("invalid history page request")
	}
	s.filesMu.RLock()
	defer s.filesMu.RUnlock()
	first := sort.Search(len(s.entries), func(i int) bool {
		return s.entries[i].at > req.After && s.entries[i].at >= now.Add(-system.HistoryRetention).UnixMilli()
	})
	var file *os.File
	defer func() {
		if file != nil {
			_ = file.Close()
		}
	}()
	var current *segment
	used := 2 // JSON array brackets.
	budget := system.HistoryMaxPageBytes / 2
	if req.Daily {
		budget = system.HistoryMaxDailyRawBytes
	}
	for _, e := range s.entries[first:] {
		if e.at > req.Before {
			break
		}
		if len(page.Samples) >= req.Limit || (req.Daily && page.Next > req.After && e.at/dayMillis != page.Next/dayMillis) {
			page.More = true
			break
		}
		if current != e.segment {
			if file != nil {
				_ = file.Close()
			}
			file, _ = os.Open(e.path)
			current = e.segment
		}
		if file == nil {
			s.SetError("storage_error")
			return page, errors.New("could not open journal segment")
		}
		v, size, err := readSample(file, e, now)
		if err != nil {
			s.SetError("corrupt_sample")
			page.Next = e.at
			continue
		}
		if used+size+1 > budget && len(page.Samples) > 0 {
			page.More = true
			break
		}
		page.Samples = append(page.Samples, v)
		used += size + 1
		page.Next = e.at
	}
	page.Info = s.Info()
	return page, nil
}
