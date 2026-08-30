package cmd

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"git.dragonsecurity.io/dragonrun/internal/registry"
)

// hints is what adopt manages to infer from a repo that has never heard of
// dragonrun. Anything it cannot determine falls back to a documented default
// rather than prompting -- adopt is meant to be run across many repos quickly.
type hints struct {
	name      string
	db        string
	upstream  int
	tenants   bool
	tenantWhy string
	ports     []string // host ports the old compose claimed, for the report
}

// skipDirs are never worth walking when looking for tenant provisioning: they
// are large, and a CREATE DATABASE found in a vendored dependency says nothing
// about what THIS application does.
var skipDirs = map[string]bool{
	"node_modules": true, "vendor": true, ".git": true, "dist": true,
	"ui": true, "web": true, "frontend": true, ".claude": true,
}

// createDBRE looks for runtime database provisioning in the app's own Go code.
// This is the reliable multi-tenant signal: across this fleet only two repos
// declare ADMIN_DATABASE_URL, but many more actually create tenant databases,
// and a role without CREATEDB fails at runtime rather than at registration.
var createDBRE = regexp.MustCompile(`(?i)\bCREATE\s+DATABASE\b`)

// scanForTenantDDL walks the repo's Go sources for runtime CREATE DATABASE.
// It stops at the first hit and never descends into skipDirs, so it stays cheap
// enough to run on every adopt.
func scanForTenantDDL(dir string) (string, bool) {
	var found string
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable subtree is not fatal to adoption
		}
		if d.IsDir() {
			if path != dir && (skipDirs[d.Name()] || strings.HasPrefix(d.Name(), ".")) {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".go") || strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}
		if info, err := d.Info(); err != nil || info.Size() > 2<<20 {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		if createDBRE.Match(b) {
			rel, _ := filepath.Rel(dir, path)
			found = rel
			return fs.SkipAll
		}
		return nil
	})
	return found, found != ""
}

var assignRE = regexp.MustCompile(`^\s*#?\s*([A-Z][A-Z0-9_]*)\s*=\s*(.*)$`)
var portRE = regexp.MustCompile(`^\s*-\s*"?(?:\d+\.\d+\.\d+\.\d+:)?(\d+):(\d+)"?\s*$`)

func scanHints(dir string) (hints, error) {
	h := hints{
		name:     filepath_Base(dir),
		upstream: 8080,
	}
	h.db = registry.RoleName(h.name)

	// .env wins over .env.example: it reflects what this checkout actually runs.
	for _, f := range []string{".env.example", ".env"} {
		path := filepath.Join(dir, f)
		fh, err := os.Open(path)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(fh)
		for sc.Scan() {
			m := assignRE.FindStringSubmatch(sc.Text())
			if m == nil {
				continue
			}
			key, val := m[1], strings.Trim(strings.TrimSpace(m[2]), `"'`)
			switch key {
			case "BIND_PORT", "PORT", "HTTP_PORT":
				if n, err := strconv.Atoi(val); err == nil && n > 0 && n < 65536 {
					h.upstream = n
				}
			case "DB_NAME":
				if val != "" {
					h.db = registry.RoleName(val)
				}
			case "ADMIN_DATABASE_URL", "TENANT_DATABASE_URL":
				// Present at all -- even commented out in the template -- means
				// this app creates tenant databases at runtime and needs CREATEDB.
				h.tenants, h.tenantWhy = true, key+" in "+f
			}
		}
		fh.Close()
	}

	// An env template is the weaker signal; the code is the strong one.
	if !h.tenants {
		if where, ok := scanForTenantDDL(dir); ok {
			h.tenants, h.tenantWhy = true, "CREATE DATABASE in "+where
		}
	}
	// A control/tenant split is often visible in the database name alone.
	if !h.tenants && strings.HasSuffix(h.db, "_control") {
		h.tenants, h.tenantWhy = true, "database name ends in _control"
	}

	// Record what the old stack was binding, so the report can tell the user
	// exactly which compose project to stop.
	if fh, err := os.Open(filepath.Join(dir, "docker-compose.yml")); err == nil {
		defer fh.Close()
		sc := bufio.NewScanner(fh)
		for sc.Scan() {
			if m := portRE.FindStringSubmatch(sc.Text()); m != nil {
				h.ports = append(h.ports, m[1])
			}
		}
	}
	return h, nil
}

var adoptCmd = &cobra.Command{
	Use:     "adopt [path]",
	GroupID: groupProject,
	Short:   "Register the repo in the current directory, inferring its settings",
	Long: `Reads .env / .env.example / docker-compose.yml to work out the project
name, control database, host port, and whether it provisions tenant databases
at runtime, then registers it.

Nothing in the repo is modified unless you pass --write, and even then only
.env is touched — the existing docker-compose.yml is left in place as a
fallback until you are happy.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := os.Getwd()
		if err != nil {
			return err
		}
		if len(args) > 0 {
			if dir, err = filepath.Abs(args[0]); err != nil {
				return err
			}
		}
		c, err := load()
		if err != nil {
			return err
		}
		h, err := scanHints(dir)
		if err != nil {
			return err
		}
		// Detection is a heuristic; let it be overridden either way.
		if cmd.Flags().Changed("tenants") {
			h.tenants, _ = cmd.Flags().GetBool("tenants")
			h.tenantWhy = "forced by --tenants"
		}
		if err := registry.ValidName(h.name); err != nil {
			return fmt.Errorf("directory name is not usable as a project name: %w — pass `dragonrun register <name>` instead", err)
		}

		// DB_NAME in these repos is frequently a template leftover -- four
		// projects in this fleet all declare "dragon". Falling back to the
		// project name keeps adopt usable across a directory sweep instead of
		// failing on the second repo that inherited the placeholder.
		if other, taken := c.DBTaken(h.db, h.name); taken {
			fmt.Printf("note: database %q is already owned by %q — using %q instead\n",
				h.db, other, registry.RoleName(h.name))
			h.db = registry.RoleName(h.name)
		}

		fmt.Printf("inferred from %s\n", dir)
		fmt.Printf("  name      %s\n", h.name)
		fmt.Printf("  database  %s\n", h.db)
		fmt.Printf("  upstream  host:%d\n", h.upstream)
		if h.tenants {
			fmt.Printf("  tenants   true  (%s)\n", h.tenantWhy)
		} else {
			fmt.Printf("  tenants   false\n")
		}
		if len(h.ports) > 0 {
			fmt.Printf("  its own compose currently claims host ports: %s\n", strings.Join(h.ports, ", "))
			fmt.Printf("  stop it first:  (cd %s && docker compose down)\n", dir)
		}
		if adoptDryRun {
			fmt.Println("\n--dry-run: nothing was changed")
			return nil
		}

		// Reuse register's logic wholesale rather than duplicating provisioning.
		regUpstream, regDB, regTenants, regPath = h.upstream, h.db, h.tenants, dir
		if err := registerCmd.Flags().Set("db", h.db); err != nil {
			return err
		}
		if err := registerCmd.Flags().Set("upstream", strconv.Itoa(h.upstream)); err != nil {
			return err
		}
		if err := registerCmd.Flags().Set("tenants", strconv.FormatBool(h.tenants)); err != nil {
			return err
		}
		fmt.Println()
		suppressEnvHint = adoptWrite
		if err := registerCmd.RunE(registerCmd, []string{h.name}); err != nil {
			return err
		}

		if adoptWrite {
			envWrite, envFile = true, filepath.Join(dir, ".env")
			fmt.Println()
			return envCmd.RunE(envCmd, []string{h.name})
		}
		fmt.Printf("\nReview then apply:  dragonrun env %s --write\n", h.name)
		return nil
	},
}

var (
	adoptDryRun bool
	adoptWrite  bool
)

func init() {
	adoptCmd.Flags().Bool("tenants", false, "override tenant detection (grants or withholds CREATEDB)")
	adoptCmd.Flags().BoolVar(&adoptDryRun, "dry-run", false, "show what would be registered and exit")
	adoptCmd.Flags().BoolVarP(&adoptWrite, "write", "w", false, "also merge the generated env into .env")
	root.AddCommand(adoptCmd)
}
