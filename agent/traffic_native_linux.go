//go:build linux

package agent

import (
	"net"
	"os"
	"strings"

	"github.com/CodeAIX/vpsmonitor/internal/accounting"
	"github.com/CodeAIX/vpsmonitor/internal/entities/traffic"
	psutilNet "github.com/shirou/gopsutil/v4/net"
)

func readNativeTraffic() (accounting.Reading, string) {
	// Docker installation/doctor also verifies NetworkMode=host. This marker
	// prevents a bare image replacement from silently counting container eth0.
	if _, err := os.Stat("/.dockerenv"); err == nil && os.Getenv("VPSMONITOR_NETWORK_SCOPE") != "host" {
		return accounting.Reading{}, traffic.NamespaceMismatch
	}
	route, err := accounting.ReadBounded("/proc/net/route", 1024*1024)
	if err != nil {
		return accounting.Reading{}, traffic.InterfaceRequired
	}
	counters, err := psutilNet.IOCounters(true)
	if err != nil {
		return accounting.Reading{}, traffic.ExecFailed
	}
	available := map[string]bool{}
	for _, c := range counters {
		available[c.Name] = true
	}
	name, err := accounting.SelectInterface(os.Getenv("VPSMONITOR_TRAFFIC_INTERFACE"), available, accounting.DefaultRoutes(string(route)))
	if err != nil {
		return accounting.Reading{}, traffic.InterfaceRequired
	}
	iface, err := net.InterfaceByName(name)
	if err != nil {
		return accounting.Reading{}, traffic.InterfaceRequired
	}
	boot, err := accounting.ReadBounded("/proc/sys/kernel/random/boot_id", 256)
	if err != nil {
		return accounting.Reading{}, traffic.ExecFailed
	}
	for _, c := range counters {
		if c.Name == name {
			return accounting.Reading{Name: name, Index: iface.Index, Hardware: iface.HardwareAddr.String(), BootID: strings.TrimSpace(string(boot)), RX: c.BytesRecv, TX: c.BytesSent}, ""
		}
	}
	return accounting.Reading{}, traffic.InterfaceRequired
}
