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
		// Rewritten on every up so a changed domain, a new built-in service,
		// or a change to what dragonrun generates is picked up without a
		// separate command -- project site files included, which otherwise
		// keep whatever `register` wrote.
		if err := edge.WriteServiceSites(c); err != nil {
			return err
		}
		if err := edge.WriteAllSites(c); err != nil {
			return err
		}
		if err := stack.Compose("up", "-d", "--build"); err != nil {
			return err
		}
		// An install from before certificate lifetimes were raised is still
		// signing 12-hour leaves off a week-long intermediate. Fix it here so
		// upgrading the binary and running `up` is the whole remedy.
		if rotated, err := edge.EnsureCertLifetimes(); err != nil {
			return err
		} else if rotated {
			fmt.Println("rotated the local CA intermediate — certificates now last 90 days")
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
		//
		// The address is PRINTED rather than asserted to be loopback: a
		// network-wide rewrite normally points the domain at this machine's
		// LAN address, which is correct, and the two failures worth naming --
		// no answer at all, or an answer pointing at some other host -- both
		// read straight off the address.
		probe := "probe." + c.Domain
		addrs := dnsconf.Addrs(probe)
		switch {
		case len(addrs) == 0:
			fmt.Printf("  MISS *.%s does not resolve (probed %s)\n", c.Domain, probe)
		default:
			fmt.Printf("  %s *.%s -> %s (probed %s)\n",
				mark(dnsconf.Resolves(probe)), c.Domain, strings.Join(addrs, ", "), probe)
			for _, ip := range addrs {
				if !dnsconf.Local(ip) {
					fmt.Printf("       %s is not an address on this machine — the edge cannot answer there\n", ip)
				}
			}
		}

		// A LAN-bound edge is the one piece of stack state that is a security
		// decision rather than a convenience, so it is stated every time.
		fmt.Println("\nedge")
		fmt.Printf("  bind %s (ports %d, %d)\n", c.Bind, c.Ports.HTTP, c.Ports.HTTPS)
		if c.LANBound() {
			fmt.Printf("  NOTE reachable from the network — including mail.%s and pgweb.%s\n",
				c.Domain, c.Domain)
		} else {
			// Loopback edge + a domain that resolves to the LAN address is the
			// exact state where every site is unreachable from this machine
			// too, and nothing else in `status` would say so.
			for _, ip := range addrs {
				if dnsconf.Local(ip) && !dnsconf.Loopback(ip) {
					fmt.Printf("  MISS *.%s resolves to %s but the edge only listens on %s\n",
						c.Domain, ip, c.Bind)
					fmt.Printf("       run `dragonrun bind %s` (or 0.0.0.0)\n", ip)
					break
				}
			}
		}

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
			if p.Serves() {
				fmt.Printf("  %-20s https://%-24s -> host:%d%s\n",
					p.Name, p.Host, p.Upstream, tenants)
			} else {
				fmt.Printf("  %-20s %-32s db %s%s\n",
					p.Name, "(no site)", p.DB, tenants)
			}
			if p.Path != "" {
				fmt.Printf("  %-20s %s\n", "", p.Path)
			}
		}

		// The one collision dragonrun cannot prevent: two projects that were
		// registered against the same host port. Database-only projects all
		// carry 0 and are not competing for anything.
		seen := map[int][]string{}
		for _, p := range c.Sorted() {
			if p.Serves() {
				seen[p.Upstream] = append(seen[p.Upstream], p.Name)
			}
		}
		for port, names := range seen {
			if len(names) > 1 {
				fmt.Printf("\n  CONFLICT: port %d claimed by %s — move one with `dragonrun set %s --upstream <port>`\n",
					port, strings.Join(names, ", "), names[0])
			}
		}

		return reportOrphans(c)
	},
}

var downVolumes bool

func init() {
	downCmd.Flags().BoolVarP(&downVolumes, "volumes", "v", false, "also delete data volumes (destroys ALL project data)")
	root.AddCommand(upCmd, downCmd, logsCmd, statusCmd)
}
