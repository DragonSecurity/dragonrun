package cmd

import (
	"fmt"
	"path/filepath"
	"strings"

	"git.dragonsecurity.io/dragonrun/internal/edge"
	"git.dragonsecurity.io/dragonrun/internal/registry"
	"git.dragonsecurity.io/dragonrun/internal/services"
	"git.dragonsecurity.io/dragonrun/internal/stack"
)

// filepath_Base is filepath.Base normalised the way project names are: a
// directory called "Eyrie" or "my_app" should still find project "eyrie" /
// "my-app" rather than reporting it unregistered.
func filepath_Base(p string) string {
	return strings.ToLower(strings.ReplaceAll(filepath.Base(p), "_", "-"))
}

// writeServiceSites renders the built-in hostnames and reports any it had to
// skip because a project already serves that name. Skipping is quiet in the
// edge package on purpose -- it has no business printing -- but a missing
// mail.test or auth.test with no explanation is the kind of thing that costs
// an afternoon.
func writeServiceSites(c *registry.Config) error {
	skipped, err := edge.WriteServiceSites(c)
	if err != nil {
		return err
	}
	for _, s := range skipped {
		fmt.Printf("warning: built-in site %s not served — rename the project to free it\n", s)
	}
	return nil
}

// prepareStack renders everything the containers read, and generates any
// service credential this install does not have yet.
//
// Run before EVERY compose up, not just the first: an install created before
// keycloak, openbao and dex existed has no passwords for them, and the compose
// file refuses to start without the values in stack/.env. Upgrading the binary
// and running `dragonrun up` is meant to be the whole remedy.
func prepareStack(c *registry.Config) error {
	changed, err := c.EnsureServiceSecrets()
	if err != nil {
		return err
	}
	if changed {
		if err := c.Save(); err != nil {
			return err
		}
	}
	// Re-extract so an upgraded binary never drives a stale compose file.
	if _, err := stack.Extract(); err != nil {
		return err
	}
	if err := stack.WriteEnv(c); err != nil {
		return err
	}
	// Rewritten on every up so a changed domain, a new built-in service, or a
	// newly registered project reaches the generated configs without a
	// separate command -- project site files included, which otherwise keep
	// whatever `register` wrote.
	if err := services.Render(c); err != nil {
		return err
	}
	if err := writeServiceSites(c); err != nil {
		return err
	}
	return edge.WriteAllSites(c)
}

// startStack brings the containers up in two phases.
//
// Keycloak, dex and OpenBao each need a postgres role and a database that only
// dragonrun can create, and compose has no way to say "run this between these
// two containers". So the cluster comes up first, dragonrun provisions against
// it, and then everything else starts against storage that already exists.
// Doing it in one phase means three containers crash-looping on their first
// connection until the restart policy happens to catch up.
func startStack(c *registry.Config) error {
	// pgbouncer depends on postgres being HEALTHY, so naming it here is what
	// makes compose wait for the cluster rather than just for the container.
	if err := stack.Compose("up", "-d", "--build", "postgres", "pgbouncer"); err != nil {
		return err
	}
	if err := services.EnsureDatabases(c); err != nil {
		return err
	}
	return stack.Compose("up", "-d", "--build")
}
