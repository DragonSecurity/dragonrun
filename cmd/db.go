package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"git.dragonsecurity.io/dragonrun/internal/provision"
	"git.dragonsecurity.io/dragonrun/internal/registry"
	"git.dragonsecurity.io/dragonrun/internal/stack"
)

var dbCmd = &cobra.Command{
	Use:     "db",
	GroupID: groupProject,
	Short:   "Inspect and manage a project's databases",
}

var dbListCmd = &cobra.Command{
	Use:   "list [name]",
	Short: "List the control database and every tenant database",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := load()
		if err != nil {
			return err
		}
		p, err := mustProject(c, args)
		if err != nil {
			return err
		}
		if !stack.Running() {
			return fmt.Errorf("the stack is not running — run `dragonrun up` first")
		}
		dbs, err := provision.Databases(c, p)
		if err != nil {
			return err
		}
		for _, d := range dbs {
			kind := "tenant"
			if d == p.DB {
				kind = "control"
			}
			fmt.Printf("  %-8s %s\n", kind, d)
		}
		if len(dbs) == 0 {
			fmt.Println("  none")
		}
		return nil
	},
}

// dbResetCmd is the shared-cluster replacement for `docker compose down -v`.
// That gesture is gone now that the volume is shared, and this is what takes
// its place: drop everything this project owns and recreate the control DB.
var dbResetCmd = &cobra.Command{
	Use:   "reset [name]",
	Short: "Drop the project's databases (control + all tenants) and recreate",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := load()
		if err != nil {
			return err
		}
		p, err := mustProject(c, args)
		if err != nil {
			return err
		}
		if !stack.Running() {
			return fmt.Errorf("the stack is not running — run `dragonrun up` first")
		}

		dbs, err := provision.Databases(c, p)
		if err != nil {
			return err
		}
		fmt.Printf("This will DESTROY %d database(s) belonging to %q:\n", len(dbs), p.Name)
		for _, d := range dbs {
			fmt.Println("  ", d)
		}
		if !dbYes {
			fmt.Printf("\nType the project name to confirm: ")
			in := bufio.NewReader(os.Stdin)
			line, _ := in.ReadString('\n')
			if strings.TrimSpace(line) != p.Name {
				return fmt.Errorf("aborted")
			}
		}

		dropped, err := provision.DropDatabases(c, p)
		if err != nil {
			return err
		}
		for _, d := range dropped {
			fmt.Println("dropped", d)
		}
		if err := provision.EnsureDB(c, p); err != nil {
			return err
		}
		fmt.Println("recreated", p.DB)
		return nil
	},
}

// dbHardenCmd re-applies CONNECT privileges across the project's databases.
// Tenant databases are created by the running application, so dragonrun is not
// in the loop when they appear -- and they arrive with template1's permissive
// default. Run this after a burst of tenant provisioning.
var dbHardenCmd = &cobra.Command{
	Use:   "harden [name]",
	Short: "Re-apply CONNECT privileges to runtime-created tenant databases",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := load()
		if err != nil {
			return err
		}
		if !stack.Running() {
			return fmt.Errorf("the stack is not running — run `dragonrun up` first")
		}
		targets := c.Sorted()
		if len(args) > 0 {
			p, err := mustProject(c, args)
			if err != nil {
				return err
			}
			targets = targets[:0]
			targets = append(targets, p)
		}
		for _, p := range targets {
			if err := provision.Harden(c, p); err != nil {
				return err
			}
			fmt.Println("hardened", p.Name)
		}
		return nil
	},
}

var psqlCmd = &cobra.Command{
	Use:     "psql [name] [database]",
	GroupID: groupProject,
	Short:   "Open psql as the project's own role, through pgbouncer",
	Long: `Connects with the project's credentials rather than the superuser's, so
what you can do here is exactly what the application can do.

Pass a database name to attach to a tenant database instead of the control one.`,
	Args: cobra.MaximumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := load()
		if err != nil {
			return err
		}
		var nameArgs []string
		if len(args) > 0 {
			nameArgs = args[:1]
		}
		p, err := mustProject(c, nameArgs)
		if err != nil {
			return err
		}
		if !stack.Running() {
			return fmt.Errorf("the stack is not running — run `dragonrun up` first")
		}
		db := p.DB
		if len(args) > 1 {
			db = args[1]
		}
		return provision.Psql(c, p, db)
	},
}

// dbCreateCmd makes a tenant database by hand. Applications normally create
// these themselves at runtime; this is for poking at one without booting the
// app. The name is forced under the project's prefix so a hand-made database
// cannot escape the namespace that `db reset` and the isolation guard rely on.
var dbCreateCmd = &cobra.Command{
	Use:   "create <name> [tenant]",
	Short: "Create a tenant database under the project's prefix",
	Args:  cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := load()
		if err != nil {
			return err
		}
		p, err := mustProject(c, args[:1])
		if err != nil {
			return err
		}
		if !stack.Running() {
			return fmt.Errorf("the stack is not running — run `dragonrun up` first")
		}
		if !p.Tenants {
			return fmt.Errorf("%q is not a multi-tenant project — its role has NOCREATEDB\n"+
				"  grant it with:  dragonrun register %s --tenants", p.Name, p.Name)
		}
		if len(args) < 2 {
			return fmt.Errorf("give the tenant a name, e.g. `dragonrun db create %s acme`", p.Name)
		}
		db := p.TenantPrefix() + registry.RoleName(args[1])
		if err := provision.CreateTenant(c, p, db); err != nil {
			return err
		}
		fmt.Printf("created %s (owner %s)\n", db, p.Role)
		return nil
	},
}

// dbDropCmd removes a single tenant database. `db reset` destroys everything a
// project owns, which is too blunt when one tenant has gone bad.
var dbDropCmd = &cobra.Command{
	Use:   "drop <name> <tenant>",
	Short: "Drop one tenant database",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := load()
		if err != nil {
			return err
		}
		p, err := mustProject(c, args[:1])
		if err != nil {
			return err
		}
		if !stack.Running() {
			return fmt.Errorf("the stack is not running — run `dragonrun up` first")
		}
		db := args[1]
		// Accept either the bare tenant name or the full database name, but
		// never let this reach outside the project's namespace.
		if !strings.HasPrefix(db, p.TenantPrefix()) {
			db = p.TenantPrefix() + registry.RoleName(db)
		}
		if db == p.DB {
			return fmt.Errorf("%q is the control database, not a tenant — use `dragonrun db reset %s`", db, p.Name)
		}
		if err := provision.DropOne(c, db); err != nil {
			return err
		}
		fmt.Println("dropped", db)
		return nil
	},
}

var dbYes bool

func init() {
	dbResetCmd.Flags().BoolVarP(&dbYes, "yes", "y", false, "skip the confirmation prompt")
	dbCmd.AddCommand(dbListCmd, dbCreateCmd, dbDropCmd, dbResetCmd, dbHardenCmd)
	root.AddCommand(dbCmd, psqlCmd)
}
