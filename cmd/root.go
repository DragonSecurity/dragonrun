// Package cmd holds the dragonrun CLI.
//
// dragonrun runs ONE shared dev stack -- postgres, pgbouncer, mailpit, pgweb,
// caddy, dnsmasq -- and hands each project a generated environment pointing at
// it. Applications keep running on the host under mprocs; dragonrun never
// supervises them.
package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"git.dragonsecurity.io/dragonrun/internal/registry"
	"git.dragonsecurity.io/dragonrun/internal/stack"
)

var root = &cobra.Command{
	Use:   "dragonrun",
	Short: "One shared postgres/mail/edge stack for every local project",
	Long: `dragonrun replaces the per-project docker-compose stack with a single
shared one, and gives each project a hostname instead of a port.

  postgres   one cluster, a role + database per project (plus tenant databases)
  pgbouncer  wildcard routing, so runtime-created tenant databases just work
  mailpit    one inbox on the canonical 1025/8025
  pgweb      one UI, superuser, switch databases from the browser
  caddy      https://<project>.test -> the app on your host
  dnsmasq    *.test -> 127.0.0.1, no per-project DNS work

Apps keep running on the host under mprocs. dragonrun only owns the infra.

Commands are grouped by what they touch. The first install of the BINARY is not
one of them -- that is: go install git.dragonsecurity.io/dragonrun@latest
Afterwards, "dragonrun update" does the same thing without the module path.`,
	SilenceUsage:  true,
	SilenceErrors: true,
}

// Command groups, in the order they appear in help. They encode the scope of
// each command: machine state outlives the stack, the stack outlives a project.
const (
	groupMachine = "machine"
	groupStack   = "stack"
	groupProject = "project"
)

func init() {
	root.AddGroup(
		&cobra.Group{ID: groupMachine, Title: "Machine (DNS, certificates, dragonrun's own state):"},
		&cobra.Group{ID: groupStack, Title: "Stack (containers and volumes):"},
		&cobra.Group{ID: groupProject, Title: "Projects:"},
	)
}

func Execute() {
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// load returns the registry, refusing to continue if dragonrun has never been
// installed -- every other command depends on the cluster secrets it holds.
func load() (*registry.Config, error) {
	c, err := registry.Load()
	if err != nil {
		return nil, err
	}
	if !c.Installed() {
		return nil, fmt.Errorf("this machine is not set up yet -- run `dragonrun init`")
	}
	// Every compose invocation must agree with the stored DNS mode, so this
	// lives in the one place all commands pass through.
	stack.SetDNSMode(c.DNS)
	return c, nil
}

// mustProject resolves a project by name, or falls back to the current
// directory's name so `dragonrun env` works with no arguments inside a repo.
func mustProject(c *registry.Config, args []string) (registry.Project, error) {
	name := ""
	if len(args) > 0 {
		name = args[0]
	} else {
		wd, err := os.Getwd()
		if err != nil {
			return registry.Project{}, err
		}
		name = filepath_Base(wd)
	}
	p, ok := c.Get(name)
	if !ok {
		return registry.Project{}, fmt.Errorf("no project %q registered -- run `dragonrun register %s` or `dragonrun adopt`", name, name)
	}
	return p, nil
}
