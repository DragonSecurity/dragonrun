package cmd

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"git.dragonsecurity.io/dragonrun/internal/registry"
)

// Vars builds the environment a project needs to talk to the shared stack.
//
// The split between the two database URLs is not cosmetic. DATABASE_URL goes
// through pgbouncer in TRANSACTION pooling mode, which is what makes hundreds
// of app connections cheap.
//
// ADMIN_DATABASE_URL goes DIRECT, for session semantics rather than for DDL:
// a bare CREATE DATABASE does survive the pooler, but transaction pooling
// breaks advisory locks, LISTEN/NOTIFY, session GUCs and temp tables -- and
// tenant provisioning code typically takes an advisory lock to serialise
// migrations. Teardown is the other half: pgbouncer's own idle server
// connections block DROP DATABASE until they are terminated.
func Vars(c *registry.Config, p registry.Project) map[string]string {
	auth := url.UserPassword(p.Role, p.Password).String()

	// The data plane deliberately uses localhost, NOT a .test hostname.
	//
	// Apps run on the same host as the stack, so a DNS name buys nothing and
	// costs availability: in `external` DNS mode every .test lookup depends on
	// the network resolver, so `db.test` stops resolving the moment the machine
	// is off that network or the resolver is down -- and every project fails to
	// start. localhost is immune to all of it.
	//
	// Only BASE_URL keeps the hostname, because that one genuinely needs DNS:
	// it is what the browser asks for and what caddy routes on.
	dbHost := "localhost"
	pooled := fmt.Sprintf("postgres://%s@%s:%d/%s?sslmode=disable", auth, dbHost, c.Ports.Bouncer, p.DB)
	direct := fmt.Sprintf("postgres://%s@%s:%d/postgres?sslmode=disable", auth, dbHost, c.Ports.Postgres)

	// caddy serves on the configured https port; only annotate the URL when it
	// is not the default, or every generated BASE_URL grows a pointless ":443".
	base := "https://" + p.Host
	if c.Ports.HTTPS != 443 {
		base = fmt.Sprintf("https://%s:%d", p.Host, c.Ports.HTTPS)
	}

	v := map[string]string{
		"DATABASE_URL":       pooled,
		"RIVER_DATABASE_URL": pooled,
		"SMTP_SERVER":        "localhost",
		"SMTP_PORT":          fmt.Sprint(c.Ports.SMTP),
	}
	// A database-only project has nothing to bind and no URL a browser could
	// ask for. Emitting BIND_PORT=0 and a BASE_URL that resolves to nothing
	// would be worse than omitting them: it reads as configuration.
	if p.Serves() {
		v["BASE_URL"] = base
		v["BIND_HOST"] = "localhost"
		v["BIND_PORT"] = fmt.Sprint(p.Upstream)
	}
	if p.Tenants {
		v["ADMIN_DATABASE_URL"] = direct
	}
	return v
}

var envCmd = &cobra.Command{
	Use:     "env [name]",
	GroupID: groupProject,
	Short:   "Print the project's environment (or merge it into .env)",
	Long: `Emits the variables pointing this project at the shared stack.

Defaults to the project matching the current directory name, so inside a repo
` + "`dragonrun env --write`" + ` is usually the whole command.

--write MERGES: listed keys are replaced, everything else in .env — secrets,
feature flags, anything hand-set — is preserved.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := load()
		if err != nil {
			return err
		}
		p, err := mustProject(c, args)
		if err != nil {
			return err
		}
		vars := Vars(c, p)

		keys := make([]string, 0, len(vars))
		for k := range vars {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		if !envWrite {
			for _, k := range keys {
				if envExport {
					fmt.Printf("export %s=%q\n", k, vars[k])
				} else {
					fmt.Printf("%s=%s\n", k, vars[k])
				}
			}
			return nil
		}

		target := envFile
		if target == "" {
			dir := p.Path
			if dir == "" {
				if dir, err = os.Getwd(); err != nil {
					return err
				}
			}
			target = filepath.Join(dir, ".env")
		}
		changed, err := mergeEnvFile(target, vars, keys)
		if err != nil {
			return err
		}
		fmt.Printf("wrote %s (%d keys set)\n", target, changed)
		return nil
	},
}

// mergeEnvFile rewrites only the managed keys, leaving every other line --
// comments, blank lines, hand-set secrets -- byte-identical.
//
// Three cases, in priority order:
//   - key has a live assignment      -> rewrite it in place
//   - key exists only as a comment   -> insert the live value just below it,
//     so `# ADMIN_DATABASE_URL=  # superuser DSN...` keeps its documentation
//     next to the value instead of the value landing 30 lines away
//   - key is absent entirely         -> append in a marked block
//
// A commented line is never uncommented: that would change the meaning of a
// line the user deliberately disabled, and could resurrect a stale value.
func mergeEnvFile(path string, vars map[string]string, keys []string) (int, error) {
	src, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return 0, err
	}
	var orig []string
	if len(src) > 0 {
		orig = strings.Split(strings.TrimRight(string(src), "\n"), "\n")
	}

	// Pass one: which managed keys already have a LIVE assignment? Only keys
	// without one may be anchored to a comment, otherwise a key that is both
	// commented and set would get written twice.
	live := map[string]bool{}
	for _, line := range orig {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		if k, _, ok := strings.Cut(t, "="); ok {
			if k = strings.TrimSpace(k); vars[k] != "" {
				live[k] = true
			}
		}
	}

	// Pass two: rewrite.
	var out []string
	seen := map[string]bool{}
	for _, line := range orig {
		t := strings.TrimSpace(line)

		if strings.HasPrefix(t, "#") {
			out = append(out, line)
			// `# KEY=anything` for a key we manage and that is not set live.
			if m := commentedRE.FindStringSubmatch(t); m != nil {
				k := m[1]
				if _, managed := vars[k]; managed && !live[k] && !seen[k] {
					out = append(out, k+"="+vars[k])
					seen[k] = true
				}
			}
			continue
		}
		if t != "" {
			if k, _, ok := strings.Cut(t, "="); ok {
				k = strings.TrimSpace(k)
				if v, managed := vars[k]; managed && !seen[k] {
					out = append(out, k+"="+v)
					seen[k] = true
					continue
				}
			}
		}
		out = append(out, line)
	}

	var missing []string
	for _, k := range keys {
		if !seen[k] {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		if len(out) > 0 && strings.TrimSpace(out[len(out)-1]) != "" {
			out = append(out, "")
		}
		out = append(out, "# ---- dragonrun (managed) ----")
		for _, k := range missing {
			out = append(out, k+"="+vars[k])
			seen[k] = true
		}
	}

	// Write via a temp file so an interrupted run cannot truncate a .env that
	// holds the only copy of a generated secret.
	tmp := path + ".dragonrun.tmp"
	if err := os.WriteFile(tmp, []byte(strings.Join(out, "\n")+"\n"), 0o600); err != nil {
		return 0, err
	}
	if err := os.Rename(tmp, path); err != nil {
		return 0, err
	}
	return len(seen), nil
}

// commentedRE matches a disabled assignment such as `# ADMIN_DATABASE_URL=` or
// `#DATABASE_URL=postgres://...`, capturing the key.
var commentedRE = regexp.MustCompile(`^#\s*([A-Z][A-Z0-9_]*)\s*=`)

var (
	envWrite  bool
	envExport bool
	envFile   string
)

func init() {
	envCmd.Flags().BoolVarP(&envWrite, "write", "w", false, "merge into the project's .env")
	envCmd.Flags().BoolVar(&envExport, "export", false, "print as shell export statements")
	envCmd.Flags().StringVar(&envFile, "file", "", "target file for --write (default <project>/.env)")
	root.AddCommand(envCmd)
}
