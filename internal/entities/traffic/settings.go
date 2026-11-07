package traffic

import "errors"

// Settings contains only portable, user-editable fields. It intentionally has
// no source counters, collector status, credentials or system database IDs.
type Settings struct {
	Enabled         bool   `json:"enabled" yaml:"enabled"`
	Iface           string `json:"iface" yaml:"iface"`
	QuotaBytes      uint64 `json:"quota_bytes" yaml:"quota_bytes"`
	CountMode       string `json:"count_mode" yaml:"count_mode"`
	BillingDay      int    `json:"billing_day" yaml:"billing_day"`
	BillingTimezone string `json:"billing_timezone" yaml:"billing_timezone"`
	DisplayUnit     string `json:"display_unit" yaml:"display_unit"`
}

func (s Settings) Validate() error {
	if (s.Iface != "" && !ValidInterface(s.Iface)) || s.QuotaBytes > MaxSafeBytes ||
		(s.CountMode != "tx" && s.CountMode != "rx_tx") || s.BillingDay < 1 || s.BillingDay > 28 ||
		s.BillingTimezone != "UTC" || (s.DisplayUnit != "GB" && s.DisplayUnit != "GiB") {
		return errors.New("invalid portable traffic settings")
	}
	return nil
}
