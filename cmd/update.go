package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/mod/semver"
)

// latest is the Go module proxy's answer for a module's newest release.
//
// The proxy rather than the GitHub API because it is the SAME source `go
// install ...@latest` resolves against: if the proxy has not caught up with a
// brand new tag yet, `update` reporting the older version is the truth, since
// installing is exactly what it would get. It also needs no token and has no
// rate limit worth worrying about.
type latest struct {
	Version string    `json:"Version"`
	Time    time.Time `json:"Time"`
}

func fetchLatest(ctx context.Context, module string) (latest, error) {
	var l latest
	// Module paths are case-encoded for the proxy; dragonrun's is all
	// lowercase, but a fork's might not be.
	url := "https://proxy.golang.org/" + escapeModule(module) + "/@latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return l, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return l, fmt.Errorf("could not reach the module proxy: %w", err)
	}
	// Closing a response body cannot fail in a way this could act on, but it
	// is not optional either: the connection leaks without it.
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return l, fmt.Errorf("module proxy returned %s for %s — has anything been tagged yet?",
			resp.Status, module)
	}
	if err := json.NewDecoder(resp.Body).Decode(&l); err != nil {
		return l, err
	}
	return l, nil
}

// escapeModule applies the proxy's case encoding: an uppercase letter becomes
// "!" plus its lowercase form, so two paths differing only in case cannot
// collide on a case-insensitive filesystem.
func escapeModule(m string) string {
	var b strings.Builder
	for _, r := range m {
		if r >= 'A' && r <= 'Z' {
			b.WriteByte('!')
			b.WriteRune(r + ('a' - 'A'))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// installDir is where `go install` will actually put the new binary. It is not
// necessarily where the running one lives -- a binary from a release tarball
// dropped in /usr/local/bin stays there, shadowing the update -- and silently
// updating a copy that is not on PATH is the worst outcome.
func installDir() string {
	if out, err := exec.Command("go", "env", "GOBIN").Output(); err == nil {
		if d := strings.TrimSpace(string(out)); d != "" {
			return d
		}
	}
	if out, err := exec.Command("go", "env", "GOPATH").Output(); err == nil {
		if d := strings.TrimSpace(string(out)); d != "" {
			return filepath.Join(d, "bin")
		}
	}
	return ""
}

var updateCheck bool

var updateCmd = &cobra.Command{
	Use:     "update",
	GroupID: groupMachine,
	Short:   "Check for a newer dragonrun and install it",
	Long: `Asks the Go module proxy for the newest tagged release and, unless --check
is given, installs it with ` + "`go install`" + ` -- the same command the README
documents, so this never leaves you with a binary installed a way you did not
choose.

It updates the BINARY only. Nothing about the running stack, the registry or
any registered project is touched, and no container is restarted. Run
` + "`dragonrun up`" + ` afterwards if a release changes the embedded stack.

Requires the Go toolchain. If dragonrun came from a release archive instead,
--check still works and prints where to get the new one.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		// Deliberately not load(): checking for a new version is useful on a
		// machine that has never run `dragonrun init`.
		module, current := Module(), Release()

		ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
		defer cancel()
		l, err := fetchLatest(ctx, module)
		if err != nil {
			return err
		}

		fmt.Printf("current  %s\n", current)
		fmt.Printf("latest   %s (%s)\n", l.Version, l.Time.Format("2006-01-02"))

		switch {
		case !semver.IsValid(current):
			// An unstamped or locally built binary has nothing to compare, so
			// treat the release as newer rather than claiming to be current.
			fmt.Printf("\nthis is not a released build — installing %s would replace it\n", l.Version)
		case semver.Compare(current, l.Version) >= 0:
			fmt.Println("\nalready on the latest release")
			return nil
		default:
			fmt.Printf("\n%s is available\n", l.Version)
		}

		target := module + "@" + l.Version
		if updateCheck {
			fmt.Printf("\ninstall it with:\n  go install %s\n", target)
			return nil
		}

		if _, err := exec.LookPath("go"); err != nil {
			return fmt.Errorf("the Go toolchain is not on PATH, so this cannot self-install — "+
				"download the archive for your platform from the release page, or install Go and "+
				"run `go install %s`", target)
		}

		// Warn BEFORE installing: after the fact the user has a new binary
		// somewhere and an old one still answering to `dragonrun`.
		if dir := installDir(); dir != "" {
			if self, err := os.Executable(); err == nil {
				if resolved, err := filepath.EvalSymlinks(self); err == nil {
					self = resolved
				}
				if filepath.Dir(self) != dir {
					fmt.Printf("\nnote: this binary is %s, but `go install` writes to %s\n", self, dir)
					fmt.Printf("      the old one will keep answering unless %s comes first on PATH\n", dir)
				}
			}
		}

		fmt.Printf("\ngo install %s\n", target)
		install := exec.CommandContext(cmd.Context(), "go", "install", target)
		install.Stdout, install.Stderr = os.Stderr, os.Stderr // keep stdout clean for scripts
		if err := install.Run(); err != nil {
			return fmt.Errorf("go install failed: %w", err)
		}
		fmt.Printf("\ninstalled %s — run `dragonrun version` to confirm\n", l.Version)
		return nil
	},
}

func init() {
	updateCmd.Flags().BoolVar(&updateCheck, "check", false,
		"report whether an update exists and exit without installing")
	root.AddCommand(updateCmd)
}
