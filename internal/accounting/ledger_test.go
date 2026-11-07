package accounting

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func reading(rx, tx uint64) Reading {
	return Reading{Name: "ens7", Index: 3, Hardware: "aa:bb:cc:dd:ee:ff", BootID: "boot-a", RX: rx, TX: tx}
}
func instant() time.Time { return time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC) }

func TestLedgerRestartUsesPersistedBaselineAndNeverBootTotals(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	first, err := l.Record(reading(100000, 200000), instant())
	if err != nil {
		t.Fatal(err)
	}
	if first.Days["2026-11-07"].RX != 0 {
		t.Fatal("counted pre-install bytes")
	}
	if _, err = l.Record(reading(100300, 200700), instant().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	l.Close()
	l, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	s, err := l.Record(reading(100500, 201000), instant().Add(5*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if d := s.Days["2026-11-07"]; d.RX != 500 || d.TX != 1000 || s.ID != first.ID {
		t.Fatalf("bad durable totals: %+v", d)
	}
	s, err = l.Record(reading(100500, 201000), instant().Add(6*time.Minute))
	if err != nil || s.Days["2026-11-07"].RX != 500 {
		t.Fatal("repeat observation changed totals", err)
	}
}

func TestFailedCommitDoesNotAdvanceCountersOrTotals(t *testing.T) {
	l, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	l.Record(reading(10, 20), instant())
	persist := l.write
	l.write = func(string, []byte) error { return errors.New("disk full") }
	if _, err = l.Record(reading(30, 50), instant().Add(time.Minute)); err == nil {
		t.Fatal("write failure accepted")
	}
	if l.State().Baseline.RX != 10 {
		t.Fatal("baseline moved after failed write")
	}
	l.write = persist
	s, err := l.Record(reading(40, 70), instant().Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if d := s.Days["2026-11-07"]; d.RX != 30 || d.TX != 50 {
		t.Fatalf("failed commit lost or doubled bytes: %+v", d)
	}
}

func TestBootResetNeverCreatesHugeOrNegativeUsage(t *testing.T) {
	l, _ := Open(t.TempDir())
	defer l.Close()
	l.Record(reading(50000, 90000), instant())
	l.Record(reading(50100, 90200), instant().Add(time.Minute))
	r := reading(5, 10)
	r.BootID = "boot-b"
	s, err := l.Record(r, instant().Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if d := s.Days["2026-11-07"]; d.RX != 100 || d.TX != 200 || !d.Partial {
		t.Fatalf("reset became traffic: %+v", d)
	}
	r.RX = 25
	r.TX = 40
	s, err = l.Record(r, instant().Add(3*time.Minute))
	if err != nil || s.Days["2026-11-07"].RX != 120 {
		t.Fatal("did not resume after baseline reset", err)
	}
}

func TestCrossDayOutageRetainsUnallocatedBytesWithoutFabricatingDailySplit(t *testing.T) {
	l, _ := Open(t.TempDir())
	defer l.Close()
	at := time.Date(2026, 11, 6, 23, 50, 0, 0, time.UTC)
	l.Record(reading(100, 200), at)
	s, err := l.Record(reading(600, 1000), at.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if s.UnallocatedRX != 500 || s.UnallocatedTX != 800 || !s.Days["2026-11-07"].Partial || s.Days["2026-11-07"].RX != 0 {
		t.Fatal("invented daily allocation", s)
	}
}

func TestUTCDateDoesNotFollowHostLocation(t *testing.T) {
	l, _ := Open(t.TempDir())
	defer l.Close()
	loc := time.FixedZone("UTC+8", 8*3600)
	at := time.Date(2026, 11, 7, 7, 59, 55, 0, loc)
	s, err := l.Record(reading(0, 0), at)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Days["2026-11-06"]; !ok {
		t.Fatal("used local calendar day")
	}
}

func TestCorruptionClockRollbackAndConcurrentWriterAreRejected(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if other, err := Open(dir); err == nil {
		other.Close()
		t.Fatal("second writer acquired same ledger")
	}
	l.Record(reading(10, 20), instant())
	if _, err = l.Record(reading(15, 25), instant().Add(-time.Minute)); err == nil {
		t.Fatal("clock rollback accepted")
	}
	l.Close()
	if err = os.WriteFile(filepath.Join(dir, "ledger-v1.json"), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if other, err := Open(dir); err == nil {
		other.Close()
		t.Fatal("corruption replaced with empty state")
	}
}

func TestDayRetentionDoesNotEraseCurrentLedgerIdentity(t *testing.T) {
	l, _ := Open(t.TempDir())
	defer l.Close()
	s, _ := l.Record(reading(1, 1), instant())
	id := s.ID
	for n := 1; n <= 410; n++ {
		s, _ = l.Record(reading(uint64(n+1), uint64(n+1)), instant().AddDate(0, 0, n))
	}
	if len(s.Days) != 400 || s.ID != id {
		t.Fatal("wrong retention or identity", len(s.Days))
	}
}

func TestNativeBaselineCanExceedJavaScriptSafeInteger(t *testing.T) {
	l, _ := Open(t.TempDir())
	defer l.Close()
	base := uint64(1) << 60
	if _, err := l.Record(reading(base, base), instant()); err != nil {
		t.Fatal(err)
	}
	s, err := l.Record(reading(base+17, base+29), instant().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if d := s.Days["2026-11-07"]; d.RX != 17 || d.TX != 29 {
		t.Fatal("lost integer precision", d)
	}
}
