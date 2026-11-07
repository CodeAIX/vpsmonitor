package accounting

import (
	"fmt"
	"strings"
	"testing"
)

func TestNineteenInterfacesSelectOnlyPublicDefault(t *testing.T) {
	available := map[string]bool{"ens7": true}
	var routes strings.Builder
	routes.WriteString("Iface Destination Gateway Flags RefCnt Use Metric Mask\nens7 00000000 01020304 0003 0 0 100 00000000\n")
	for n := 0; n < 18; n++ {
		name := fmt.Sprintf("veth%d", n)
		available[name] = true
		fmt.Fprintf(&routes, "%s 00000000 01020304 0003 0 0 1 00000000\n", name)
	}
	got, err := SelectInterface("auto", available, DefaultRoutes(routes.String()))
	if err != nil || got != "ens7" {
		t.Fatal(got, err)
	}
}

func TestAmbiguousRoutesRequireExplicitChoiceAndBondIsEligible(t *testing.T) {
	available := map[string]bool{"ens7": true, "bond0": true, "docker0": true}
	if _, err := SelectInterface("", available, []string{"ens7", "bond0"}); err == nil {
		t.Fatal("ambiguous interfaces summed")
	}
	if got, err := SelectInterface("bond0", available, nil); err != nil || got != "bond0" {
		t.Fatal(got, err)
	}
	if _, err := SelectInterface("docker0", available, nil); err == nil {
		t.Fatal("counted docker bridge")
	}
	if _, err := SelectInterface("missing", available, nil); err == nil {
		t.Fatal("missing interface accepted")
	}
}
