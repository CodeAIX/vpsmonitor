// Package accounting owns native, durable host-interface traffic accounting.
package accounting

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/CodeAIX/vpsmonitor/internal/entities/traffic"
)

var ErrClockBackwards = errors.New("host clock moved backwards")
var ErrInterfaceChanged = errors.New("traffic uplink changed; select an explicit interface")

const MaxDays = 400
const maxStateBytes = 256 * 1024

type Reading struct {
	Name, BootID, Hardware string
	Index                  int
	RX, TX                 uint64
}

type Day struct {
	RX      uint64 `json:"rx"`
	TX      uint64 `json:"tx"`
	Partial bool   `json:"partial,omitempty"`
}

type State struct {
	Version       int            `json:"version"`
	ID            string         `json:"id"`
	Created       int64          `json:"created"`
	Updated       int64          `json:"updated"`
	Baseline      Reading        `json:"baseline"`
	Days          map[string]Day `json:"days"`
	UnallocatedRX uint64         `json:"unallocated_rx"`
	UnallocatedTX uint64         `json:"unallocated_tx"`
}

type Ledger struct {
	path  string
	lock  *os.File
	state State
	write func(string, []byte) error
}

// Open rejects corruption rather than silently replacing a saved baseline.
// One writer per volume prevents two Agent processes from double accounting.
func Open(dir string) (*Ledger, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	lock, err := lockFile(filepath.Join(dir, ".writer.lock"))
	if err != nil {
		return nil, err
	}
	l := &Ledger{path: filepath.Join(dir, "ledger-v1.json"), lock: lock, write: atomicWrite}
	b, err := ReadBounded(l.path, maxStateBytes)
	if errors.Is(err, os.ErrNotExist) {
		return l, nil
	}
	if err != nil {
		l.Close()
		return nil, err
	}
	if len(b) > maxStateBytes || json.Unmarshal(b, &l.state) != nil || !validState(l.state) {
		l.Close()
		return nil, errors.New("invalid traffic ledger; preserve the volume and inspect it")
	}
	return l, nil
}

func validState(s State) bool {
	if s.Version != 1 || len(s.ID) != 32 || s.Created <= 0 || s.Updated < s.Created || s.Days == nil || len(s.Days) > MaxDays || !traffic.ValidInterface(s.Baseline.Name) {
		return false
	}
	if _, err := hex.DecodeString(s.ID); err != nil {
		return false
	}
	for key, day := range s.Days {
		if _, err := traffic.ParseDay(key); err != nil || day.RX > traffic.MaxSafeBytes || day.TX > traffic.MaxSafeBytes-day.RX {
			return false
		}
	}
	return true
}

func (l *Ledger) Close() error {
	if l.lock == nil {
		return nil
	}
	err := l.lock.Close()
	l.lock = nil
	return err
}

func (l *Ledger) State() State {
	s := l.state
	s.Days = make(map[string]Day, len(l.state.Days))
	for k, v := range l.state.Days {
		s.Days[k] = v
	}
	return s
}

// Record commits totals and the new baseline together. No mutation survives a
// failed write. First observation establishes a baseline, never counts boot totals.
func (l *Ledger) Record(r Reading, now time.Time) (State, error) {
	if !traffic.ValidInterface(r.Name) || r.BootID == "" || r.Index <= 0 {
		return State{}, errors.New("invalid native counter reading")
	}
	stamp := now.Unix()
	s := l.State()
	if stamp <= 0 || (s.Updated > 0 && stamp < s.Updated) {
		return State{}, ErrClockBackwards
	}
	key := now.UTC().Format(time.DateOnly)
	if s.Version == 0 {
		id := make([]byte, 16)
		if _, err := rand.Read(id); err != nil {
			return State{}, err
		}
		s = State{Version: 1, ID: hex.EncodeToString(id), Created: stamp, Days: map[string]Day{key: {Partial: true}}}
	} else {
		if r.Name != s.Baseline.Name {
			return State{}, ErrInterfaceChanged
		}
		day := s.Days[key]
		continuous := r.BootID == s.Baseline.BootID && r.Index == s.Baseline.Index && r.Hardware == s.Baseline.Hardware && r.RX >= s.Baseline.RX && r.TX >= s.Baseline.TX
		if continuous {
			rx, tx := r.RX-s.Baseline.RX, r.TX-s.Baseline.TX
			// Ordinary boundary sampling uses the interval end (<=30s target).
			// A long outage spanning dates has no trustworthy daily allocation.
			if time.Unix(s.Updated, 0).UTC().Format(time.DateOnly) != key && stamp-s.Updated > 90 {
				if rx > traffic.MaxSafeBytes-s.UnallocatedRX || tx > traffic.MaxSafeBytes-s.UnallocatedTX {
					return State{}, errors.New("counter overflow")
				}
				s.UnallocatedRX += rx
				s.UnallocatedTX += tx
				day.Partial = true
				oldKey := time.Unix(s.Updated, 0).UTC().Format(time.DateOnly)
				old := s.Days[oldKey]
				old.Partial = true
				s.Days[oldKey] = old
			} else {
				if rx > traffic.MaxSafeBytes-day.RX || tx > traffic.MaxSafeBytes-day.TX || day.RX+rx > traffic.MaxSafeBytes-(day.TX+tx) {
					return State{}, errors.New("counter overflow")
				}
				day.RX += rx
				day.TX += tx
			}
		} else {
			day.Partial = true
		}
		s.Days[key] = day
	}
	s.Baseline = r
	s.Updated = stamp
	cutoff := now.UTC().AddDate(0, 0, -(MaxDays - 1)).Format(time.DateOnly)
	for day := range s.Days {
		if day < cutoff {
			delete(s.Days, day)
		}
	}
	b, err := json.Marshal(s)
	if err != nil || len(b) > maxStateBytes {
		return State{}, errors.New("traffic ledger exceeds storage limit")
	}
	if err := l.write(l.path, b); err != nil {
		return State{}, fmt.Errorf("persist traffic ledger: %w", err)
	}
	l.state = s
	return l.State(), nil
}

func atomicWrite(path string, b []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".ledger-*")
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
	if err := os.Rename(f.Name(), path); err != nil {
		return err
	}
	d, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer d.Close()
	// Directory sync is required for a durable rename on the supported Linux FS.
	return d.Sync()
}

// ReadBounded is used for kernel metadata and never consumes unbounded input.
func ReadBounded(path string, max int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, errors.New("metadata exceeds limit")
	}
	return b, nil
}
