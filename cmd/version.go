package cmd

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

// Overwritten at release time by GoReleaser's ldflags. The defaults are what a
// `go build` or `go run` produces, and saying "dev" plainly is more useful than
// printing a version number that was never released.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// Version renders the one-line version string used by both `dragonrun version`
// and `dragonrun --version`.
func Version() string {
	return fmt.Sprintf("dragonrun %s (%s) built %s, %s/%s with %s",
		version, commit, date, runtime.GOOS, runtime.GOARCH, runtime.Version())
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
