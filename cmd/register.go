package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"git.dragonsecurity.io/dragonrun/internal/edge"
	"git.dragonsecurity.io/dragonrun/internal/provision"
	"git.dragonsecurity.io/dragonrun/internal/registry"
	"git.dragonsecurity.io/dragonrun/internal/stack"
)

var (
	regUpstream int
	regHost     string
	regDB       string
	regTenants  bool
	regPath     string
	regNoSite   bool
)

// reconcile makes the stack match one registry entry: role, database, route
// and bookmark. register, set and sync all go through it, so a project cannot
// end up half-provisioned by whichever command happened to touch it last.
func reconcile(c *registry.Config, p registry.Project) error {
	if err := provision.EnsureRole(c, p); err != nil {
		return err
	}
	if err := provision.EnsureDB(c, p); err != nil {
		return err
	}
	// RemoveSite rather than skip: a project that HAD a route and just lost one
	// must stop being served, not keep the file it was registered with.
	if !p.Serves() {
		if err := edge.RemoveSite(p.Name); err != nil {
			return err
		}
	} else if err := edge.WriteSite(p); err != nil {
		return err
	}
	return edge.WriteBookmark(c, p)
}

var registerCmd = &cobra.Command{
	Use:     "register <name>",
	GroupID: groupProject,
	Short:   "Create a project's role, database, hostname and route",
	Long: `Provisions everything a project needs in the shared stack:

  a login role (with CREATEDB when --tenants is set)
  a control database created from template1
  https://<name>.test routed to a host port
  a pgweb bookmark

Re-running is safe: it reconciles rather than recreating, so the password and
any existing data survive. That is also how you CHANGE a registered project,
though ` + "`dragonrun set`" + ` says it more plainly.

--no-site (or --upstream 0) registers a project with no HTTP route at all: a
migration scratch database, a worker, a schema two other repos share. It gets a
role, a database and a bookmark, claims no host port, and can never collide
with a project that does serve.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		if err := registry.ValidName(name); err != nil {
			return err
		}
		c, err := load()
		if err != nil {
			return err
		}
		if !stack.Running() {
			return fmt.Errorf("the stack is not running — run `dragonrun up` first")
		}

		p, existing := c.Get(name)
		if !existing {
			pw, err := registry.Secret(18)
			if err != nil {
				return err
			}
			p = registry.Project{
				Name:     name,
				Role:     registry.RoleName(name),
				Password: pw,
				DB:       registry.RoleName(name),
				Upstream: 8080,
			}
		}
		if regDB != "" {
			p.DB = registry.RoleName(regDB)
		}
		if regHost != "" {
			p.Host = regHost
		}
		if p.Host == "" {
			p.Host = name + "." + c.Domain
		}
		// `--upstream 0` says the same thing as --no-site: no port, so nothing
		// to route to. Both spellings are accepted so neither is a silent
		// no-op, and naming a real port is how a database-only project gets a
		// route BACK -- without that it would keep resetting to 0 forever.
		if cmd.Flags().Changed("upstream") {
			p.Upstream = regUpstream
			p.NoSite = regUpstream == 0
		}
		if cmd.Flags().Changed("no-site") {
			p.NoSite = regNoSite
		}
		if p.NoSite {
			p.Upstream = 0
		} else if p.Upstream == 0 {
			p.NoSite = true
		}
		if cmd.Flags().Changed("tenants") {
			p.Tenants = regTenants
		}
		if regPath != "" {
			p.Path = regPath
		} else if p.Path == "" {
			p.Path, _ = os.Getwd()
		}

		// Refuse rather than warn: sharing a control database is silent data
		// corruption, not an inconvenience.
		if other, taken := c.DBTaken(p.DB, p.Name); taken {
			return fmt.Errorf("database %q is already owned by project %q\n"+
				"  two projects must never share a control database — their migrations would interleave\n"+
				"  pick another with:  dragonrun register %s --db %s", p.DB, other, p.Name, registry.RoleName(p.Name))
		}

		// Warn rather than refuse: two projects can legitimately share a port
		// if they are never run at the same time.
		if other, taken := c.UpstreamTaken(p.Upstream, p.Name); taken {
			fmt.Printf("note: host port %d is also claimed by %q — only one can run at a time\n",
				p.Upstream, other)
		}

		if err := reconcile(c, p); err != nil {
			return err
		}
		// Keeps the built-in sites present even if DRAGONRUN_HOME was wiped.
		if err := edge.WriteServiceSites(c); err != nil {
			return err
		}

		c.Projects[p.Name] = p
		if err := c.Save(); err != nil {
			return err
		}
		if err := edge.Reload(); err != nil {
			fmt.Println("warning: caddy reload failed:", err)
		}

		verb := "registered"
		if existing {
			verb = "updated"
		}
		if p.Serves() {
			fmt.Printf("%s %s\n  https://%s -> host:%d\n  database %s (role %s)\n",
				verb, p.Name, p.Host, p.Upstream, p.DB, p.Role)
		} else {
			fmt.Printf("%s %s\n  no site — database only\n  database %s (role %s)\n",
				verb, p.Name, p.DB, p.Role)
		}
		if p.Tenants {
			fmt.Printf("  tenant databases: %s*  (role has CREATEDB)\n", p.TenantPrefix())
		}
		// Suppressed when `new` or `adopt -w` is about to write it anyway.
		if !suppressEnvHint {
			fmt.Printf("\nWrite its environment with:  dragonrun env %s --write\n", p.Name)
		}
		return nil
	},
}

var removeCmd = &cobra.Command{
	Use:     "delete <name>",
	Aliases: []string{"remove", "rm"},
	GroupID: groupProject,
	Short:   "Unregister a project, optionally dropping its databases",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := load()
		if err != nil {
			return err
		}
		p, ok := c.Get(args[0])
		if !ok {
			return fmt.Errorf("no project %q registered", args[0])
		}

		if rmData {
			if !stack.Running() {
				return fmt.Errorf("the stack must be running to drop databases")
			}
			dropped, err := provision.DropDatabases(c, p)
			if err != nil {
				return err
			}
			for _, d := range dropped {
				fmt.Println("dropped database", d)
			}
			if err := provision.DropRole(c, p); err != nil {
				return err
			}
			fmt.Println("dropped role", p.Role)
		} else {
			fmt.Printf("keeping database %s and role %s — re-run with --data to drop them\n", p.DB, p.Role)
		}

		if err := edge.RemoveSite(p.Name); err != nil {
			return err
		}
		if err := edge.RemoveBookmark(p.Name); err != nil {
			return err
		}
		delete(c.Projects, p.Name)
		if err := c.Save(); err != nil {
			return err
		}
		_ = edge.Reload()
		fmt.Println("unregistered", p.Name)
		return nil
	},
}

// syncCmd rebuilds every derived artefact from the registry. The registry is
// the source of truth; this is how you recover after editing it, restoring a
// backup, or losing DRAGONRUN_HOME's generated files.
var syncCmd = &cobra.Command{
	Use:     "sync",
	GroupID: groupProject,
	Short:   "Rebuild roles, databases, routes and bookmarks from the registry",
	RunE: func(cmd *cobra.Command, _ []string) error {
		c, err := load()
		if err != nil {
			return err
		}
		if !stack.Running() {
			return fmt.Errorf("the stack is not running — run `dragonrun up` first")
		}
		for _, p := range c.Sorted() {
			if err := reconcile(c, p); err != nil {
				return err
			}
			fmt.Println("synced", p.Name)
		}
		return edge.Reload()
	},
}

var rmData bool

// suppressEnvHint stops register repeating a next step its caller is already
// taking.
var suppressEnvHint bool

func init() {
	registerCmd.Flags().IntVarP(&regUpstream, "upstream", "p", 8080, "host port the app listens on")
	registerCmd.Flags().StringVar(&regHost, "host", "", "hostname to serve (default <name>.test)")
	registerCmd.Flags().StringVar(&regDB, "db", "", "control database name (default <name>)")
	registerCmd.Flags().BoolVar(&regTenants, "tenants", false, "grant CREATEDB for runtime tenant databases")
	registerCmd.Flags().StringVar(&regPath, "path", "", "repository path (default current directory)")
	registerCmd.Flags().BoolVar(&regNoSite, "no-site", false, "database only: no hostname, no route, no host port")
	removeCmd.Flags().BoolVar(&rmData, "data", false, "also drop the databases and role")
	root.AddCommand(registerCmd, removeCmd, syncCmd)
}
