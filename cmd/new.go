package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/spf13/cobra"

	"git.dragonsecurity.io/dragonrun/internal/registry"
)

// newCmd is the greenfield counterpart to adopt: adopt infers from an existing
// per-project stack, new starts from nothing. Neither requires the repo to
// contain a docker-compose.yml, which is the point -- a new project needs no
// stack scaffolding of its own at all.
var newCmd = &cobra.Command{
	Use:     "new <name>",
	GroupID: groupProject,
	Short:   "Create a project from scratch: role, database, hostname and .env",
	Long: `Provisions a brand new project in the shared stack and writes its .env.

This is what replaces copying a docker-compose.yml, a postgres/Dockerfile and a
pgbouncer/ directory into every new repo. There is nothing to carry: the stack
already exists, and this hands the project its credentials.

  dragonrun new automechanic --tenants -p 8181

Use ` + "`dragonrun adopt`" + ` instead for a repo that already has its own stack.`,
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
		if _, exists := c.Get(name); exists {
			return fmt.Errorf("project %q already exists — use `dragonrun show %s`, or `dragonrun register %s` to change it", name, name, name)
		}

		dir := newPath
		if dir == "" {
			if dir, err = os.Getwd(); err != nil {
				return err
			}
		}
		if dir, err = filepath.Abs(dir); err != nil {
			return err
		}

		// Delegate to register so provisioning lives in exactly one place.
		if err := registerCmd.Flags().Set("upstream", strconv.Itoa(newUpstream)); err != nil {
			return err
		}
		if err := registerCmd.Flags().Set("tenants", strconv.FormatBool(newTenants)); err != nil {
			return err
		}
		if err := registerCmd.Flags().Set("path", dir); err != nil {
			return err
		}
		if newDB != "" {
			if err := registerCmd.Flags().Set("db", newDB); err != nil {
				return err
			}
		}
		suppressEnvHint = !newNoEnv
		if err := registerCmd.RunE(registerCmd, []string{name}); err != nil {
			return err
		}

		if newNoEnv {
			fmt.Printf("\nWrite its environment when ready:  dragonrun env %s --write\n", name)
			return nil
		}
		envWrite, envFile = true, filepath.Join(dir, ".env")
		fmt.Println()
		if err := envCmd.RunE(envCmd, []string{name}); err != nil {
			return err
		}
		fmt.Printf("\nNo compose file needed. Start your app on port %d and it is live at https://%s.%s\n",
			newUpstream, name, c.Domain)
		return nil
	},
}

var (
	newUpstream int
	newTenants  bool
	newDB       string
	newPath     string
	newNoEnv    bool
)

func init() {
	newCmd.Flags().IntVarP(&newUpstream, "upstream", "p", 8080, "host port the app will listen on")
	newCmd.Flags().BoolVar(&newTenants, "tenants", false, "grant CREATEDB for runtime tenant databases")
	newCmd.Flags().StringVar(&newDB, "db", "", "control database name (default <name>)")
	newCmd.Flags().StringVar(&newPath, "path", "", "repository path (default current directory)")
	newCmd.Flags().BoolVar(&newNoEnv, "no-env", false, "do not write .env")
	root.AddCommand(newCmd)
}
