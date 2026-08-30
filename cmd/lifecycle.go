package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"git.dragonsecurity.io/dragonrun/internal/dnsconf"
	"git.dragonsecurity.io/dragonrun/internal/edge"
	"git.dragonsecurity.io/dragonrun/internal/registry"
	"git.dragonsecurity.io/dragonrun/internal/stack"
)

var upCmd = &cobra.Command{
	Use:     "up",
	GroupID: groupStack,
	Short:   "Start the shared stack",
	RunE: func(cmd *cobra.Command, _ []string) error {
		c, err := load()
		if err != nil {
			return err
		}
		if err := stack.RequireDocker(); err != nil {
			return err
		}
		// Re-extract so an upgraded binary never drives a stale compose file.
		if _, err := stack.Extract(); err != nil {
			return err
		}
		if err := stack.WriteEnv(c); err != nil {
			return err
		}
		// Rewritten on every up so a changed domain, or a new built-in
		// service, is picked up without a separate command.
		if err := edge.WriteServiceSites(c); err != nil {
			return err
		}
		if err := stack.Compose("up", "-d", "--build"); err != nil {
			return err
		}
		return edge.Reload()
	},
}

var downCmd = &cobra.Command{
	Use:     "down",
	GroupID: groupStack,
	Short:   "Stop the shared stack (data volumes are kept)",
	RunE: func(cmd *cobra.Command, args []string) error {
		if _, err := load(); err != nil {
			return err
		}
		a := []string{"down"}
		if downVolumes {
			// This destroys every project's data in one go, which is why it is
			// never implied by a bare `down`.
			fmt.Println("WARNING: -v destroys the postgres volume — every project's data.")
			a = append(a, "-v")
		}
		return stack.Compose(a...)
	},
}

var logsCmd = &cobra.Command{
	Use:     "logs [service]",
	GroupID: groupStack,
	Short:   "Tail stack logs",
	Args:    cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if _, err := load(); err != nil {
			return err
		}
		return stack.Compose(append([]string{"logs", "-f", "--tail=100"}, args...)...)
	},
}

var statusCmd = &cobra.Command{
	Use:     "status",
	GroupID: groupStack,
	Short:   "Show stack health, DNS wiring and registered projects",
	RunE: func(cmd *cobra.Command, _ []string) error {
		c, err := load()
		if err != nil {
			return err
		}

		fmt.Println("stack")
		if !stack.Running() {
			fmt.Println("  down — run `dragonrun up`")
		} else {
			out, err := stack.ComposeOut("ps", "--format",
				"{{.Service}}\t{{.State}}\t{{.Publishers}}")
			if err != nil {
				return err
			}
			for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
				f := strings.Split(line, "\t")
				if len(f) < 2 {
					continue
				}
				fmt.Printf("  %-10s %s\n", f[0], f[1])
			}
		}

		fmt.Println("\ndns")
		mark := func(ok bool) string {
			if ok {
				return "ok  "
			}
			return "MISS"
		}
		fmt.Printf("  mode %s\n", c.DNS)
		if c.DNS == registry.DNSExternal {
			// In external mode the resolver file must be ABSENT: it takes
			// precedence over the system resolver and would shadow the
			// network-wide rewrite this mode depends on.
			_, err := os.Stat(dnsconf.Path(c.Domain))
			fmt.Printf("  %s %s absent (external mode)\n", mark(os.IsNotExist(err)), dnsconf.Path(c.Domain))
		} else {
			fmt.Printf("  %s %s (port %d)\n", mark(dnsconf.Installed(c.Domain, c.Ports.DNS)), dnsconf.Path(c.Domain), c.Ports.DNS)
		}
		// Any name under the domain proves the wildcard works; nothing in a
		// generated env depends on this name resolving.
		probe := "probe." + c.Domain
		fmt.Printf("  %s *.%s resolves to 127.0.0.1 (probed %s)\n",
			mark(dnsconf.Resolves(probe)), c.Domain, probe)

		fmt.Println("\nprojects")
		if len(c.Projects) == 0 {
			fmt.Println("  none — cd into a repo and run `dragonrun adopt`")
			return nil
		}
		for _, p := range c.Sorted() {
			tenants := ""
			if p.Tenants {
				tenants = "  [multi-tenant]"
			}
			fmt.Printf("  %-20s https://%-24s -> host:%d%s\n",
				p.Name, p.Host, p.Upstream, tenants)
			if p.Path != "" {
				fmt.Printf("  %-20s %s\n", "", p.Path)
			}
		}

		// The one collision dragonrun cannot prevent: two projects that were
		// registered against the same host port.
		seen := map[int][]string{}
		for _, p := range c.Sorted() {
			seen[p.Upstream] = append(seen[p.Upstream], p.Name)
		}
		for port, names := range seen {
			if len(names) > 1 {
				fmt.Printf("\n  CONFLICT: port %d claimed by %s — `dragonrun register --upstream` one of them\n",
					port, strings.Join(names, ", "))
			}
		}
		return nil
	},
}

var downVolumes bool

func init() {
	downCmd.Flags().BoolVarP(&downVolumes, "volumes", "v", false, "also delete data volumes (destroys ALL project data)")
	root.AddCommand(upCmd, downCmd, logsCmd, statusCmd)
}
