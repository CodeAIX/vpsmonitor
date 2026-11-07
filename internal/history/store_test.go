package history

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fxamacker/cbor/v2"
	"github.com/CodeAIX/vpsmonitor/internal/entities/system"
	"github.com/stretchr/testify/require"
)

func historySample(at time.Time, cpu float64) system.HistorySample {
	return system.HistorySample{At: at.UnixMilli(), Stats: system.Stats{Cpu: cpu, MemPct: 24.5}}
}

func TestSevenDayJournalSurvivesRestartAndPages(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Minute)
	dir := t.TempDir()
	s, err := Open(dir, DefaultMaxBytes, now.Add(-8*24*time.Hour))
	require.NoError(t, err)
	for _, d := range []time.Duration{-8 * 24 * time.Hour, -7 * 24 * time.Hour, -6 * 24 * time.Hour, -time.Minute, 0} {
		at := now.Add(d)
		require.NoError(t, s.Append(historySample(at, 42), at))
	}
	require.Equal(t, 4, s.Info().Samples, "eight-day-old sample must be removed")
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
	require.NoError(t, os.WriteFile(s.entries[1].path, []byte("broken gzip"), 0600))
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
