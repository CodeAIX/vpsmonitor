package health

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCheckDoesNotRefreshConnectionTimestamp(t *testing.T) {
	old := healthFile
	t.Cleanup(func() { healthFile = old })
	healthFile = filepath.Join(t.TempDir(), "connection")
	if err := Check(); !os.IsNotExist(err) {
		t.Fatalf("missing timestamp should fail the check: %v", err)
	}
	if err := Update(); err != nil {
		t.Fatal(err)
	}
	if err := Check(); err != nil {
		t.Fatal(err)
	}
	stale := time.Now().Add(-2 * time.Minute)
	if err := os.Chtimes(healthFile, stale, stale); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := Check(); err == nil {
			t.Fatal("checking a stale timestamp must not make it healthy")
		}
	}
}
