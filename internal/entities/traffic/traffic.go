// Package traffic defines the optional vnStat wire contract. Byte values are
// absolute daily totals, never deltas. Existing system metric keys are untouched.
package traffic

import (
	"errors"
	"strings"
	"time"
	"unicode"
)

const (
	SchemaVersion              = 1
	MaxSafeBytes        uint64 = 1<<53 - 1
	MaxInterfaces              = 16
	MaxDays                    = 400
	MaxPayload                 = 256 * 1024
	OK                         = "ok"
	Initializing               = "initializing"
	Disabled                   = "disabled"
	Missing                    = "vnstat_missing"
	NoDatabase                 = "no_database"
	NoInterfaces               = "no_interfaces"
	UnsupportedTimebase        = "unsupported_timebase"
	UnsupportedVersion         = "unsupported_version"
	UnsupportedPlatform        = "unsupported_platform"
	Timeout                    = "exec_timeout"
	InvalidJSON                = "invalid_json"
	PermissionDenied           = "permission_denied"
	TooLarge                   = "payload_too_large"
	ExecFailed                 = "exec_failed"
	InvalidConfig              = "invalid_config"
)

type DailyBucket struct {
	Day     string `json:"day" cbor:"0,keyasint"`
	RxBytes uint64 `json:"rx" cbor:"1,keyasint"`
	TxBytes uint64 `json:"tx" cbor:"2,keyasint"`
}

type InterfaceDaily struct {
	Name            string        `json:"name" cbor:"0,keyasint"`
	Days            []DailyBucket `json:"days" cbor:"1,keyasint"`
	SourceUpdatedAt int64         `json:"source_updated_at" cbor:"2,keyasint"`
	SourceCreatedAt int64         `json:"source_created_at" cbor:"3,keyasint"`
}

type Snapshot struct {
	SchemaVersion       uint8            `json:"v" cbor:"0,keyasint"`
	Status              string           `json:"status" cbor:"1,keyasint"`
	ObservedAt          int64            `json:"observed_at" cbor:"2,keyasint"`
	Timebase            string           `json:"timebase" cbor:"3,keyasint"`
	Interfaces          []InterfaceDaily `json:"interfaces,omitempty" cbor:"4,keyasint,omitempty"`
	ErrorCode           string           `json:"error_code,omitempty" cbor:"5,keyasint,omitempty"`
	RefreshSeconds      int64            `json:"refresh_seconds" cbor:"6,keyasint"`
	SaveIntervalSeconds int64            `json:"save_interval_seconds" cbor:"7,keyasint"`
	// This is an operator attestation, not inferred from the CLI's timezone.
	UTCValidFromDay string `json:"utc_valid_from_day,omitempty" cbor:"8,keyasint,omitempty"`
	VNStatVersion   string `json:"vnstat_version,omitempty" cbor:"9,keyasint,omitempty"`
}

func ParseDay(value string) (time.Time, error) {
	d, err := time.Parse(time.DateOnly, value)
	if err != nil || d.Format(time.DateOnly) != value {
		return time.Time{}, errors.New("invalid UTC day")
	}
	return d, nil
}

func ValidInterface(name string) bool {
	return name != "" && len(name) <= 64 && !strings.ContainsFunc(name, func(r rune) bool { return unicode.IsControl(r) })
}

// Validate is also used at the hub trust boundary; do not trust agent-side checks.
func (s *Snapshot) Validate(now time.Time) error {
	if s == nil || s.SchemaVersion != SchemaVersion || s.ObservedAt <= 0 || s.ObservedAt > now.Unix()+300 {
		return errors.New("invalid traffic snapshot")
	}
	if s.RefreshSeconds < 60 || s.RefreshSeconds > 3600 || len(s.Interfaces) > MaxInterfaces {
		return errors.New("traffic limits exceeded")
	}
	if s.Status != OK {
		switch s.Status {
		case Initializing, Disabled, Missing, NoDatabase, NoInterfaces, UnsupportedTimebase, UnsupportedVersion, UnsupportedPlatform, Timeout, InvalidJSON, PermissionDenied, TooLarge, ExecFailed, InvalidConfig:
			if len(s.Interfaces) != 0 || s.ErrorCode != s.Status {
				return errors.New("invalid traffic error")
			}
			return nil
		default:
			return errors.New("unknown traffic status")
		}
	}
	from, err := ParseDay(s.UTCValidFromDay)
	if err != nil || s.Timebase != "utc" || s.ErrorCode != "" || s.SaveIntervalSeconds < 60 || s.SaveIntervalSeconds > 3600 || len(s.Interfaces) == 0 || from.After(now) {
		return errors.New("unverified traffic source")
	}
	names := make(map[string]bool)
	for _, iface := range s.Interfaces {
		if !ValidInterface(iface.Name) || names[iface.Name] || len(iface.Days) > MaxDays || iface.SourceCreatedAt <= 0 || iface.SourceUpdatedAt < iface.SourceCreatedAt || iface.SourceUpdatedAt > now.Unix()+300 {
			return errors.New("invalid traffic interface")
		}
		names[iface.Name] = true
		days := make(map[string]bool)
		for _, day := range iface.Days {
			d, err := ParseDay(day.Day)
			if err != nil || d.Before(from) || d.After(now) || days[day.Day] || day.RxBytes > MaxSafeBytes || day.TxBytes > MaxSafeBytes || day.RxBytes > MaxSafeBytes-day.TxBytes {
				return errors.New("invalid traffic daily bucket")
			}
			days[day.Day] = true
		}
	}
	return nil
}
