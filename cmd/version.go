package cmd

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"sync"

	"github.com/spf13/cobra"
)

// Overwritten at release time by GoReleaser's ldflags.
//
// Only the RELEASE ARCHIVES get these. `go install <module>@latest` compiles
// from the module proxy and never runs GoReleaser, so a binary installed that
// way would report "dev" forever -- even though the version it was built from
// is sitting right there in the binary's own build info. fromBuildInfo fills
// them in from that, so every install path reports something true.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

var resolveOnce sync.Once

// fromBuildInfo recovers what the ldflags would have said, from what the Go
// toolchain records on its own.
//
// The two install paths carry different halves of it, which is why both are
// consulted:
//
//	go install <module>@v1.2.3   Main.Version is "v1.2.3"; no vcs.* at all,
//	                             because the proxy hands over a zip, not a repo
//	go build   (in a checkout)   Main.Version is "(devel)"; vcs.revision,
//	                             vcs.time and vcs.modified are present
func fromBuildInfo() {
	if info, ok := debug.ReadBuildInfo(); ok {
		applyBuildInfo(info)
	}
}

// applyBuildInfo is fromBuildInfo with the reading split off, because
// debug.ReadBuildInfo describes the test binary rather than dragonrun and so
// cannot exercise either install path.
func applyBuildInfo(info *debug.BuildInfo) {
	if version == "dev" && info.Main.Version != "" && info.Main.Version != "(devel)" {
		version = info.Main.Version
	}
	settings := map[string]string{}
	for _, s := range info.Settings {
		settings[s.Key] = s.Value
	}
	if commit == "none" {
		if rev := settings["vcs.revision"]; rev != "" {
			commit = rev
			if len(commit) > 7 {
				commit = commit[:7]
			}
			// A dirty tree is the difference between "this is that commit" and
			// "this is that commit plus whatever was uncommitted at the time".
			if settings["vcs.modified"] == "true" {
				commit += "-dirty"
			}
		}
	}
	if date == "unknown" {
		if t := settings["vcs.time"]; t != "" {
			date = t
		}
	}
}

// Version renders the one-line version string used by both `dragonrun version`
// and `dragonrun --version`.
func Version() string {
	resolveOnce.Do(fromBuildInfo)
	return fmt.Sprintf("dragonrun %s (%s) built %s, %s/%s with %s",
		version, commit, date, runtime.GOOS, runtime.GOARCH, runtime.Version())
}

// Release is the version alone, as a semver tag or "dev". `update` compares
// against this, and needs it without the surrounding prose.
func Release() string {
	resolveOnce.Do(fromBuildInfo)
	return version
}

// Module is the import path this binary was built from, which is what `update`
// hands back to `go install`. Read from build info rather than hard-coded so a
// fork updates from ITSELF, not from upstream.
func Module() string {
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Path != "" {
		return info.Main.Path
	}
	return "git.dragonsecurity.io/dragonrun"
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the dragonrun version",
	Run: func(cmd *cobra.Command, _ []string) {
		fmt.Println(Version())
	},
}

func init() {
	root.Version = Version()
	root.SetVersionTemplate("{{.Version}}\n")
	root.AddCommand(versionCmd)
}
