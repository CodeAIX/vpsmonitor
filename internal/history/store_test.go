package history

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/CodeAIX/vpsmonitor/internal/entities/system"
	"github.com/fxamacker/cbor/v2"
	"github.com/stretchr/testify/require"
)

func historySample(at time.Time, cpu float64) system.HistorySample {
	return system.HistorySample{At: at.UnixMilli(), Stats: system.Stats{Cpu: cpu, MemPct: 24.5}}
}

func Test31DayJournalSurvivesRestartAndPages(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Minute)
	dir := t.TempDir()
	s, err := Open(dir, DefaultMaxBytes, now.Add(-32*24*time.Hour))
	require.NoError(t, err)
	for _, d := range []time.Duration{-32 * 24 * time.Hour, -31 * 24 * time.Hour, -30 * 24 * time.Hour, -time.Minute, 0} {
		at := now.Add(d)
		require.NoError(t, s.Append(historySample(at, 42), at))
	}
	require.Equal(t, 4, s.Info().Samples, "samples outside the 31-day window must be removed")
	s, err = Open(dir, DefaultMaxBytes, now)
	require.NoError(t, err)
	require.Equal(t, 4, s.Info().Samples)
	first, err := s.Page(system.HistoryRequest{After: 0, Before: now.UnixMilli(), Limit: 2}, now)
	require.NoError(t, err)
	require.Len(t, first.Samples, 2)
	require.True(t, first.More)
	second, err := s.Page(system.HistoryRequest{After: first.Next, Before: now.UnixMilli(), Limit: 2}, now)
	require.NoError(t, err)
	require.Len(t, second.Samples, 2)
	require.False(t, second.More)
	require.Equal(t, now.UnixMilli(), second.Next)
	// A separate Hub starts from its own cursor; reads/acknowledgements do not
	// consume the Agent's files or alter another Hub's replay window.
	other, err := s.Page(system.HistoryRequest{After: 0, Before: now.UnixMilli(), Limit: 128}, now)
	require.NoError(t, err)
	require.Len(t, other.Samples, 4)
	require.Equal(t, 4, s.Info().Samples)
	for _, e := range s.entries {
		stat, err := os.Stat(e.path)
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0600), stat.Mode().Perm())
	}
}

func TestJournalMinuteIdentityClockAndStorageLimit(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Minute)
	s, err := Open(t.TempDir(), DefaultMaxBytes, now)
	require.NoError(t, err)
	require.NoError(t, s.Append(historySample(now, 1), now))
	size := s.Info().Bytes
	s.maxBytes = size*2 + 10
	require.NoError(t, s.Append(historySample(now.Add(10*time.Second), 99), now.Add(10*time.Second)))
	require.Equal(t, 1, s.Info().Samples, "one sample per actual minute")
	for n := 1; n < 4; n++ {
		at := now.Add(time.Duration(n) * time.Minute)
		require.NoError(t, s.Append(historySample(at, float64(n)), at))
	}
	require.LessOrEqual(t, s.Info().Bytes, s.maxBytes)
	require.True(t, s.Info().Limited)
	require.LessOrEqual(t, s.Info().Samples, 2)
	reopened, err := Open(s.dir, s.maxBytes, now.Add(4*time.Minute))
	require.NoError(t, err)
	require.True(t, reopened.Info().Limited, "capacity truncation must remain visible after a restart")
	require.NoError(t, s.Append(historySample(now, 1), now.Add(4*time.Minute)))
	require.Equal(t, "clock_regression", s.Info().Error)
	_, err = s.Page(system.HistoryRequest{After: 0, Before: now.UnixMilli(), Limit: 129}, now)
	require.Error(t, err)
}

func TestJournalCorruptionCannotBlockFollowingSamples(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Minute)
	s, err := Open(t.TempDir(), 0, now)
	require.NoError(t, err)
	for n := 0; n < 3; n++ {
		at := now.Add(time.Duration(n) * time.Minute)
		require.NoError(t, s.Append(historySample(at, float64(n)), at))
	}
	bad := s.entries[1]
	f, err := os.OpenFile(bad.path, os.O_WRONLY, 0600)
	require.NoError(t, err)
	_, err = f.WriteAt([]byte("broken gzip"), bad.offset)
	require.NoError(t, err)
	require.NoError(t, f.Close())
	p, err := s.Page(system.HistoryRequest{After: 0, Before: now.Add(3 * time.Minute).UnixMilli(), Limit: 128}, now.Add(3*time.Minute))
	require.NoError(t, err)
	require.Len(t, p.Samples, 2)
	require.Equal(t, s.entries[2].at, p.Next)
	require.Equal(t, "corrupt_sample", p.Info.Error)
	// Future timestamps and non-finite JSON cannot enter the journal.
	require.Error(t, s.Append(historySample(now.Add(time.Hour), 1), now))
}

func TestJournalStorageFailureAndSymlinkIsolation(t *testing.T) {
	now := time.Now().UTC()
	base := t.TempDir()
	dir := filepath.Join(base, "journal")
	s, err := Open(dir, 0, now)
	require.NoError(t, err)
	require.NoError(t, os.Remove(dir))
	require.NoError(t, os.WriteFile(dir, []byte("not a directory"), 0600))
	require.Error(t, s.Append(historySample(now, 1), now))
	require.Equal(t, "storage_error", s.Info().Error)
	dir = filepath.Join(base, "safe")
	require.NoError(t, os.Mkdir(dir, 0700))
	outside := filepath.Join(base, "outside")
	require.NoError(t, os.WriteFile(outside, []byte("unrelated private data"), 0600))
	if os.Symlink(outside, filepath.Join(dir, "1234567890123.json.gz")) == nil {
		s, err = Open(dir, 0, now)
		require.NoError(t, err)
		require.Equal(t, 0, s.Info().Samples)
	}
}

func TestJournalConcurrentMetadataAndReplay(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Minute)
	s, err := Open(t.TempDir(), 0, now)
	require.NoError(t, err)
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Go(func() {
			for n := 0; n < 30; n++ {
				_ = s.Info()
				_, err := s.Page(system.HistoryRequest{After: 0, Before: now.Add(time.Hour).UnixMilli(), Limit: 128}, now.Add(time.Hour))
				require.NoError(t, err)
			}
		})
	}
	for n := 0; n < 30; n++ {
		at := now.Add(time.Duration(n) * time.Minute)
		require.NoError(t, s.Append(historySample(at, 1), at))
	}
	wg.Wait()
	require.Equal(t, 30, s.Info().Samples)
}

func TestJournalPagesAreByteBoundedEvenWithLargeSensorMaps(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Minute)
	s, err := Open(t.TempDir(), 0, now)
	require.NoError(t, err)
	temps := map[string]float64{}
	for n := 0; n < 500; n++ {
		temps[fmt.Sprintf("%s-%d", strings.Repeat("sensor", 60), n)] = 42
	}
	for n := 0; n < 4; n++ {
		at := now.Add(time.Duration(n) * time.Minute)
		sample := historySample(at, 42)
		sample.Stats.Temperatures = temps
		require.NoError(t, s.Append(sample, at))
	}
	p, err := s.Page(system.HistoryRequest{After: 0, Before: now.Add(4 * time.Minute).UnixMilli(), Limit: 128}, now.Add(4*time.Minute))
	require.NoError(t, err)
	require.True(t, p.More)
	require.Less(t, len(p.Samples), 4)
	b, err := cbor.Marshal(p)
	require.NoError(t, err)
	require.LessOrEqual(t, len(b), system.HistoryMaxPageBytes)
}

func TestDailyJournalKeepsMinutePrecisionAndRepairsInterruptedWrite(t *testing.T) {
	dir := t.TempDir()
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	s, err := Open(dir, DefaultMaxBytes, start)
	require.NoError(t, err)
	// One file and one page for a full UTC day at ordinary sample sizes.
	for n := range system.HistoryDailySamples + 1 {
		at := start.Add(time.Duration(n) * time.Minute)
		require.NoError(t, s.Append(historySample(at, float64(n%100)), at))
	}
	require.Len(t, s.files, 2)
	now := start.Add(24 * time.Hour)
	page, err := s.Page(system.HistoryRequest{Before: now.UnixMilli(), Limit: system.HistoryDailySamples, Daily: true}, now)
	require.NoError(t, err)
	require.True(t, page.More)
	require.Len(t, page.Samples, 1440)
	require.Equal(t, start.Add(1439*time.Minute).UnixMilli(), page.Next)
	require.NoError(t, page.PackDaily())
	wire, err := cbor.Marshal(page)
	require.NoError(t, err)
	require.Less(t, len(wire), system.HistoryMaxPageBytes)
	require.NoError(t, page.UnpackDaily(system.HistoryRequest{Daily: true, Limit: system.HistoryDailySamples}))
	require.Len(t, page.Samples, 1440)
	require.Equal(t, float64(39), page.Samples[1439].Stats.Cpu)

	path := s.files[1].path
	size := s.files[1].size
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0600)
	require.NoError(t, err)
	_, err = f.Write([]byte("partial"))
	require.NoError(t, err)
	require.NoError(t, f.Close())
	s, err = Open(dir, DefaultMaxBytes, now)
	require.NoError(t, err)
	stat, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, size, stat.Size())
	require.Equal(t, 1441, s.Info().Samples)
	now = now.Add(time.Minute)
	require.NoError(t, s.Append(historySample(now, 55), now))
	require.Len(t, s.files, 2)
	require.Equal(t, path, s.files[1].path)
	page, err = s.Page(system.HistoryRequest{After: page.Next, Before: now.UnixMilli(), Limit: system.HistoryDailySamples, Daily: true}, now)
	require.NoError(t, err)
	require.Len(t, page.Samples, 2)
	require.Equal(t, float64(55), page.Samples[1].Stats.Cpu)
	require.False(t, page.More)

	// At a rolling cutoff within a UTC day, only the in-window suffix
	// can be replayed. Once the whole archive expires, unlink its file.
	oldDay := s.files[0].path
	now = start.Add(system.HistoryRetention + 12*time.Hour)
	require.NoError(t, s.Append(historySample(now, 4), now))
	require.Equal(t, start.Add(12*time.Hour).UnixMilli(), s.Info().Oldest)
	require.FileExists(t, oldDay)
	page, err = s.Page(system.HistoryRequest{Before: start.Add(24*time.Hour - time.Minute).UnixMilli(),
		Limit: system.HistoryDailySamples, Daily: true}, now)
	require.NoError(t, err)
	require.Len(t, page.Samples, 720)
	require.Equal(t, start.Add(12*time.Hour).UnixMilli(), page.Samples[0].At)
	sizeBeforePrune := s.Info().Bytes
	oldDayInfo, err := os.Stat(oldDay)
	require.NoError(t, err)
	now = start.Add(system.HistoryRetention + 24*time.Hour)
	s.Prune(now)
	require.NoFileExists(t, oldDay)
	require.Equal(t, sizeBeforePrune-oldDayInfo.Size(), s.Info().Bytes)
}

func TestLegacyMinuteCacheRemainsReadableWithDailyFiles(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 8, 4, 0, 0, 0, time.UTC)
	at := now.Add(-8 * 24 * time.Hour)
	path := filepath.Join(dir, fmt.Sprintf("%013d.json.gz", at.UnixMilli()))
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0600)
	require.NoError(t, err)
	z := gzip.NewWriter(f)
	require.NoError(t, json.NewEncoder(z).Encode(historySample(at, 41)))
	require.NoError(t, z.Close())
	require.NoError(t, f.Close())
	s, err := Open(dir, DefaultMaxBytes, now)
	require.NoError(t, err)
	require.NoError(t, s.Append(historySample(now, 42), now))
	page, err := s.Page(system.HistoryRequest{Before: now.UnixMilli(), Limit: system.HistoryMaxPageSamples}, now)
	require.NoError(t, err)
	require.Len(t, page.Samples, 2)
	require.Equal(t, float64(41), page.Samples[0].Stats.Cpu)
	require.Equal(t, float64(42), page.Samples[1].Stats.Cpu)
	require.FileExists(t, path)
}

func TestDailyJournalSplitsLargeDayWithoutDroppingMinutes(t *testing.T) {
	now := time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC)
	s, err := Open(t.TempDir(), 0, now)
	require.NoError(t, err)
	require.NoError(t, s.Append(historySample(now, 42), now))

	// Scale the budget down so its per-file limit is between one and two
	// frames. Exercise rotation and reopening without a multi-MiB fixture.
	s.maxBytes = 6 * s.Info().Bytes
	for n := 1; n < 4; n++ {
		at := now.Add(time.Duration(n) * time.Minute)
		require.NoError(t, s.Append(historySample(at, 42), at))
	}
	require.Len(t, s.files, 4)
	for _, part := range s.files {
		require.Equal(t, now.UnixMilli()/dayMillis, part.first/dayMillis)
	}
	require.False(t, s.Info().Limited)
	s, err = Open(s.dir, s.maxBytes, now.Add(4*time.Minute))
	require.NoError(t, err)
	page, err := s.Page(system.HistoryRequest{
		Before: now.Add(4 * time.Minute).UnixMilli(), Limit: system.HistoryDailySamples, Daily: true,
	}, now.Add(4*time.Minute))
	require.NoError(t, err)
	require.Len(t, page.Samples, 4)
	require.False(t, page.More)
	for n, sample := range page.Samples {
		require.Equal(t, now.Add(time.Duration(n)*time.Minute).UnixMilli(), sample.At)
	}
}
