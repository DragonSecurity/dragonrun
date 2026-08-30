package stack

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The compose file declares `name: dragonrun`, so without a per-home project
// name every instance shares containers and volumes. A scratch instance would
// then adopt the real stack's postgres volume and destroy it on `down -v`.
func TestProjectNameIsolatesInstances(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}

	t.Setenv("DRAGONRUN_HOME", filepath.Join(home, ".dragonrun"))
	if got := ProjectName(); got != "dragonrun" {
		t.Errorf("default home gave %q, want the stable name %q", got, "dragonrun")
	}

	t.Setenv("DRAGONRUN_HOME", "/tmp/scratch-a/home")
	a := ProjectName()
	t.Setenv("DRAGONRUN_HOME", "/tmp/scratch-b/home")
	b := ProjectName()

	for _, n := range []string{a, b} {
		if n == "dragonrun" {
			t.Fatalf("a scratch home produced %q — it would share volumes with the real stack", n)
		}
		if !strings.HasPrefix(n, "dragonrun-") {
			t.Errorf("project name %q should stay recognisable", n)
		}
	}
	if a == b {
		t.Errorf("two different homes both gave %q — they would share volumes", a)
	}
}
