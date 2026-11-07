package agent

import (
	"github.com/CodeAIX/vpsmonitor/internal/accounting"
	"testing"
	"time"
)

func TestNativeSnapshotIncludesStableSourceAndPartialCoverage(t *testing.T) {
	now := time.Now().UTC()
	day := now.Format(time.DateOnly)
	s := accounting.State{Version: 1, ID: "0123456789abcdef0123456789abcdef", Created: now.Add(-time.Minute).Unix(), Updated: now.Unix(), Baseline: accounting.Reading{Name: "ens7"}, Days: map[string]accounting.Day{day: {RX: 100, TX: 200, Partial: true}}}
	snapshot := nativeSnapshot(s, now)
	if err := snapshot.Validate(now); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Interfaces) != 1 || snapshot.Interfaces[0].SourceID != s.ID || !snapshot.Interfaces[0].Days[0].Partial {
		t.Fatal("native source or coverage lost")
	}
}
