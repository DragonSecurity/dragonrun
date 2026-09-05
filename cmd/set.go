package cmd

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"git.dragonsecurity.io/dragonrun/internal/edge"
	"git.dragonsecurity.io/dragonrun/internal/registry"
	"git.dragonsecurity.io/dragonrun/internal/stack"
)

var (
	setUpstream int
	setHost     string
	setDB       string
	setTenants  bool
	setSite     bool
	setNoSite   bool
	setPath     string
)

// setCmd is `register` for a project that already exists. register can do all
// of this -- it reconciles rather than recreates -- but nobody reaches for a
// command called "register" to move a port, so the capability was there and
// unfindable. This is the same reconcile path under a name that says edit.
var setCmd = &cobra.Command{
	Use:     "set <name>",
	Aliases: []string{"edit"},
	GroupID: groupProject,
	Short:   "Change a registered project's port, hostname, database or tenancy",
	Long: `Edits one registered project in place and rebuilds what changed.

The common case is two projects that both defaulted to 8080:

  dragonrun set myapp --upstream 8081

The port moves, the caddy site is rewritten, caddy reloads, and the project's
BIND_PORT and BASE_URL change with it -- re-run ` + "`dragonrun env <name> --write`" + `
to push them into its .env.

--no-site turns a project into a database-only one: the route is removed and
the host port released. --site gives it back.

Unlike ` + "`register`" + `, this refuses a project that does not exist, so a typo
cannot silently create a second one.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := load()
		if err != nil {
			return err
		}
		p, ok := c.Get(args[0])
		if !ok {
			return fmt.Errorf("no project %q registered — `dragonrun status` lists them, "+
				"`dragonrun register %s` creates one", args[0], args[0])
		}
		before := p

		touched := false
		for _, f := range []string{"upstream", "host", "db", "tenants", "site", "no-site", "path"} {
			if cmd.Flags().Changed(f) {
				touched = true
			}
		}
		if !touched {
			return fmt.Errorf("nothing to change — pass at least one of " +
				"--upstream, --host, --db, --tenants, --site/--no-site, --path")
		}
		if !stack.Running() {
			return fmt.Errorf("the stack is not running — run `dragonrun up` first")
		}

		if cmd.Flags().Changed("upstream") {
			p.Upstream = setUpstream
			// A port was named, so the project serves again -- otherwise
			// `set x --upstream 8081` on a database-only project would write
			// the port and still refuse to route to it.
			p.NoSite = setUpstream == 0
		}
		// --site and --no-site are the same switch from either end. Both are
		// offered because half the time you know what you want it to become
		// and half the time you know what you want it to stop being.
		if cmd.Flags().Changed("site") {
			p.NoSite = !setSite
		}
		if cmd.Flags().Changed("no-site") {
			p.NoSite = setNoSite
		}
		if p.NoSite {
			p.Upstream = 0
		} else if p.Upstream == 0 {
			return fmt.Errorf("--site needs a port — pass --upstream too")
		}
		if cmd.Flags().Changed("host") {
			p.Host = setHost
		}
		if cmd.Flags().Changed("tenants") {
			p.Tenants = setTenants
		}
		if cmd.Flags().Changed("path") {
			if p.Path, err = filepath.Abs(setPath); err != nil {
				return err
			}
		}
		if cmd.Flags().Changed("db") {
			p.DB = registry.RoleName(setDB)
		}

		// Same rule as register, and for the same reason: two projects sharing
		// a control database interleave their migrations in silence.
		if other, taken := c.DBTaken(p.DB, p.Name); taken {
			return fmt.Errorf("database %q is already owned by project %q", p.DB, other)
		}
		if other, taken := c.UpstreamTaken(p.Upstream, p.Name); taken {
			fmt.Printf("note: host port %d is also claimed by %q — only one can run at a time\n",
				p.Upstream, other)
		}

		if err := reconcile(c, p); err != nil {
			return err
		}
		c.Projects[p.Name] = p
		if err := c.Save(); err != nil {
			return err
		}
		if err := edge.Reload(); err != nil {
			fmt.Println("warning: caddy reload failed:", err)
		}

		for _, ch := range changes(before, p) {
			fmt.Println(" ", ch)
		}
		// The new database is created but the old one is NOT dropped: it may
		// hold the only copy of something, and this command has no business
		// deciding that. Say so rather than leaving it to be discovered.
		if before.DB != p.DB {
			fmt.Printf("\ndatabase %q is left in place — nothing points at it now\n", before.DB)
		}
		if before.Upstream != p.Upstream || before.Host != p.Host {
			fmt.Printf("\nIts environment changed:  dragonrun env %s --write\n", p.Name)
		}
		return nil
	},
}

// changes renders what actually moved, so the output is a diff rather than a
// re-print of the whole project.
func changes(a, b registry.Project) []string {
	var out []string
	site := func(p registry.Project) string {
		if !p.Serves() {
			return "no site (database only)"
		}
		return fmt.Sprintf("https://%s -> host:%d", p.Host, p.Upstream)
	}
	if site(a) != site(b) {
		out = append(out, fmt.Sprintf("site      %s  ->  %s", site(a), site(b)))
	}
	if a.DB != b.DB {
		out = append(out, fmt.Sprintf("database  %s  ->  %s", a.DB, b.DB))
	}
	if a.Tenants != b.Tenants {
		out = append(out, fmt.Sprintf("tenants   %v  ->  %v", a.Tenants, b.Tenants))
	}
	if a.Path != b.Path {
		out = append(out, fmt.Sprintf("path      %s  ->  %s", a.Path, b.Path))
	}
	if len(out) == 0 {
		out = append(out, "no change")
	}
	return out
}

func init() {
	setCmd.Flags().IntVarP(&setUpstream, "upstream", "p", 0, "host port the app listens on")
	setCmd.Flags().StringVar(&setHost, "host", "", "hostname to serve")
	setCmd.Flags().StringVar(&setDB, "db", "", "control database name")
	setCmd.Flags().BoolVar(&setTenants, "tenants", false, "grant or withhold CREATEDB for tenant databases")
	setCmd.Flags().BoolVar(&setNoSite, "no-site", false, "database only: remove the route and release the port")
	setCmd.Flags().BoolVar(&setSite, "site", true, "give a database-only project a route again (needs --upstream)")
	setCmd.Flags().StringVar(&setPath, "path", "", "repository path")
	root.AddCommand(setCmd)
}
