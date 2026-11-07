package common

import (
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/CodeAIX/vpsmonitor/internal/entities/system"
)

// Fail closed on upstream protocol changes; do not silently renumber actions
// or collide with a newly allocated CombinedData key.
func TestPlusProtocolContract(t *testing.T) {
	for expected, actual := range []WebSocketAction{GetData, CheckFingerprint, GetContainerLogs, GetContainerInfo, GetSmartData, GetSystemdInfo, GetZfsData, SyncNetworkMonitors, GetPackageUpdates, GetSystemdLogs} {
		if int(actual) != expected {
			t.Fatalf("action %d moved to %d", expected, actual)
		}
	}
	typeOf := reflect.TypeFor[system.CombinedData]()
	seen := map[int]string{}
	for index := 0; index < typeOf.NumField(); index++ {
		field := typeOf.Field(index)
		key, err := strconv.Atoi(strings.Split(field.Tag.Get("cbor"), ",")[0])
		if err != nil {
			t.Fatalf("unknown CombinedData key: %s", field.Name)
		}
		if previous := seen[key]; previous != "" {
			t.Fatalf("CBOR key %d collides: %s/%s", key, previous, field.Name)
		}
		seen[key] = field.Name
	}
	if seen[7] != "Traffic" {
		t.Fatal("CombinedData traffic key 7 changed")
	}
	for key, name := range []string{"Stats", "Info", "Containers", "SystemdServices", "Details", "SystemdServicesUpdated", "Monitors"} {
		if seen[key] != name {
			t.Fatalf("upstream key %d changed: %s", key, seen[key])
		}
	}
}
