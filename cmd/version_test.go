package cmd

import (
	"runtime/debug"
	"testing"
)

// reset puts the ldflags variables back to their unstamped defaults, since
// applyBuildInfo only fills in what a release did not.
func reset(t *testing.T) {
	t.Helper()
	v, c, d := version, commit, date
	version, commit, date = "dev", "none", "unknown"
	t.Cleanup(func() { version, commit, date = v, c, d })
}

// `go install <module>@v1.2.3` is the install path the README documents, and
// the one GoReleaser's ldflags never reach: the proxy hands over a zip, so
// there is no VCS to stamp from and only the module version is known.
func TestVersionFromModuleVersion(t *testing.T) {
	reset(t)
	applyBuildInfo(&debug.BuildInfo{
		Main: debug.Module{Path: "git.dragonsecurity.io/dragonrun", Version: "v1.2.3"},
	})
	if version != "v1.2.3" {
		t.Errorf("version = %q, want v1.2.3", version)
	}
}

// A build from a checkout has the opposite half: no usable module version, but
// a revision and a timestamp.
func TestVersionFromVCSStamps(t *testing.T) {
	reset(t)
	applyBuildInfo(&debug.BuildInfo{
		Main: debug.Module{Version: "(devel)"},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "27260678baaba789d511c63b740ae18e1dd8b420"},
			{Key: "vcs.time", Value: "2026-08-30T19:53:08Z"},
			{Key: "vcs.modified", Value: "true"},
		},
	})
	if version != "dev" {
		t.Errorf("version = %q, want dev — (devel) is not a version to report", version)
	}
	if commit != "2726067-dirty" {
		t.Errorf("commit = %q, want 2726067-dirty", commit)
	}
	if date != "2026-08-30T19:53:08Z" {
		t.Errorf("date = %q", date)
	}
}

// GoReleaser's ldflags are the most specific answer available and must not be
// overwritten by the build info sitting behind them.
func TestStampedValuesWin(t *testing.T) {
	v, c, d := version, commit, date
	version, commit, date = "v0.1.0", "2726067", "2026-08-31T18:01:31Z"
	t.Cleanup(func() { version, commit, date = v, c, d })

	applyBuildInfo(&debug.BuildInfo{
		Main:     debug.Module{Version: "v9.9.9"},
		Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "deadbeefdeadbeef"}},
	})
	if version != "v0.1.0" || commit != "2726067" {
		t.Errorf("build info overwrote the release stamps: %s (%s)", version, commit)
	}
}

// The proxy encodes uppercase so two module paths differing only in case
// cannot collide on a case-insensitive filesystem.
func TestEscapeModule(t *testing.T) {
	for in, want := range map[string]string{
		"git.dragonsecurity.io/dragonrun": "git.dragonsecurity.io/dragonrun",
		"github.com/DragonSecurity/x":     "github.com/!dragon!security/x",
	} {
		if got := escapeModule(in); got != want {
			t.Errorf("escapeModule(%q) = %q, want %q", in, got, want)
		}
	}
}
