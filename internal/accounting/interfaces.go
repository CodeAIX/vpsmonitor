package accounting

import (
	"errors"
	"strconv"
	"strings"
)

// Excluded only filters virtual/container/tunnel device families. Bond/VLAN
// uplinks remain eligible; selecting one route avoids summing parents/children.
func Excluded(name string) bool {
	if name == "lo" {
		return true
	}
	for _, prefix := range []string{"docker", "br-", "veth", "cali", "cni", "flannel", "virbr", "tailscale", "tun", "tap", "wg"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// DefaultRoutes reads IPv4 /proc/net/route. Only up, lowest-metric default
// routes are candidates. IPv6-only or ambiguous hosts require explicit choice.
func DefaultRoutes(raw string) []string {
	best := uint64(^uint64(0))
	seen := map[string]bool{}
	for _, line := range strings.Split(raw, "\n") {
		f := strings.Fields(line)
		if len(f) < 8 || f[1] != "00000000" || f[7] != "00000000" || Excluded(f[0]) {
			continue
		}
		flags, err := strconv.ParseUint(f[3], 16, 64)
		if err != nil || flags&1 == 0 {
			continue
		}
		metric, err := strconv.ParseUint(f[6], 10, 64)
		if err != nil {
			continue
		}
		if metric < best {
			best = metric
			seen = map[string]bool{}
		}
		if metric == best {
			seen[f[0]] = true
		}
	}
	result := []string{}
	for name := range seen {
		result = append(result, name)
	}
	return result
}

func SelectInterface(explicit string, available map[string]bool, defaults []string) (string, error) {
	if explicit != "" && explicit != "auto" {
		if Excluded(explicit) {
			return "", errors.New("container, loopback and tunnel interfaces cannot be the VPS accounting uplink")
		}
		if !available[explicit] {
			return "", errors.New("configured traffic interface is unavailable")
		}
		return explicit, nil
	}
	selected := ""
	for _, name := range defaults {
		if !available[name] || Excluded(name) {
			continue
		}
		if selected != "" && selected != name {
			return "", errors.New("multiple default uplinks; set VPSMONITOR_TRAFFIC_INTERFACE")
		}
		selected = name
	}
	if selected == "" {
		return "", errors.New("no unambiguous default uplink; set VPSMONITOR_TRAFFIC_INTERFACE")
	}
	return selected, nil
}
