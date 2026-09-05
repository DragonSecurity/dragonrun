package cmd

import (
	"fmt"
	"strings"

	"git.dragonsecurity.io/dragonrun/internal/edge"
	"git.dragonsecurity.io/dragonrun/internal/provision"
	"git.dragonsecurity.io/dragonrun/internal/registry"
	"git.dragonsecurity.io/dragonrun/internal/stack"
)

// orphan is something the stack is still carrying that no registered project
// accounts for.
type orphan struct {
	kind string // database, site file, certificate, bookmark
	name string
	fix  string
}

// findOrphans compares what the stack actually holds against the registry.
//
// The registry is the source of truth and everything else is a cache, but the
// cache is not self-cleaning: unregistering drops the site file and bookmark
// and leaves the DATABASE, by design, while caddy keeps a certificate for a
// hostname long after anything serves it. Both are invisible until something
// goes looking, which is how a database nobody remembers creating stays in the
// cluster for months.
func findOrphans(c *registry.Config) ([]orphan, error) {
	// owned answers "does any project account for this database", including
	// the tenant namespace each project owns at runtime.
	ownedDB := func(db string) bool {
		for _, p := range c.Projects {
			if db == p.DB || (p.Tenants && strings.HasPrefix(db, p.TenantPrefix())) {
				return true
			}
		}
		return false
	}
	ownedHost := func(host string) bool {
		for _, p := range c.Projects {
			if p.Serves() && host == p.Host {
				return true
			}
		}
		// The built-in service UIs are dragonrun's own, not orphans.
		for n := range edge.ServiceHosts {
			if host == n+"."+c.Domain {
				return true
			}
		}
		return false
	}

	var out []orphan
	if stack.Running() {
		dbs, err := provision.AllDatabases(c)
		if err != nil {
			return nil, err
		}
		for _, db := range dbs {
			if !ownedDB(db) {
				out = append(out, orphan{"database", db,
					"register something that owns it, or drop it in pgweb"})
			}
		}
		hosts, err := edge.CertHosts()
		if err != nil {
			return nil, err
		}
		for _, h := range hosts {
			if !ownedHost(h) {
				out = append(out, orphan{"certificate", h,
					"harmless; caddy drops it when the volume is recreated"})
			}
		}
	}

	sites, err := edge.SiteFiles()
	if err != nil {
		return nil, err
	}
	for _, name := range sites {
		if _, ok := c.Get(name); !ok {
			out = append(out, orphan{"site file", name + ".caddy",
				"dragonrun sync, or delete the file"})
		}
	}
	marks, err := edge.Bookmarks()
	if err != nil {
		return nil, err
	}
	for _, name := range marks {
		// `cluster` is the superuser bookmark init writes for the whole
		// cluster; it deliberately matches no project.
		if _, ok := c.Get(name); !ok && name != "cluster" {
			out = append(out, orphan{"bookmark", name + ".toml",
				"dragonrun sync, or delete the file"})
		}
	}
	return out, nil
}

// reportOrphans prints the section only when there is something to say. A
// clean machine should not grow a permanent empty heading.
func reportOrphans(c *registry.Config) error {
	orphans, err := findOrphans(c)
	if err != nil {
		return err
	}
	if len(orphans) == 0 {
		return nil
	}
	fmt.Printf("\norphans — nothing owns these (%d)\n", len(orphans))
	for _, o := range orphans {
		fmt.Printf("  %-12s %-28s %s\n", o.kind, o.name, o.fix)
	}
	return nil
}
