package ghupdate

import (
	"strings"
	"testing"
)

func TestPlusRefusesUpstreamBinaryUpdate(t *testing.T) {
	updated, err := Update(Config{})
	if updated || err == nil || !strings.Contains(err.Error(), "Docker images") {
		t.Fatalf("fork must refuse replacement with upstream executable: %v %v", updated, err)
	}
}
