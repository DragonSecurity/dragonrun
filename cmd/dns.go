package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"git.dragonsecurity.io/dragonrun/internal/dnsconf"
	"git.dragonsecurity.io/dragonrun/internal/registry"
	"git.dragonsecurity.io/dragonrun/internal/stack"
)

// ensureNoResolverFile removes /etc/resolver/<domain> when running in external
// mode.
//
// This is the whole point of the mode: /etc/resolver takes PRECEDENCE over the
// system resolver, so a leftover file silently shadows a perfectly good
// network-wide rewrite. The symptom is a resolver that looks configured, a
// dnsmasq container that looks healthy, and names that never resolve.
func ensureNoResolverFile(domain string) error {
	if !dnsconf.Supported() {
		return nil
	}
	if _, err := os.Stat(dnsconf.Path(domain)); os.IsNotExist(err) {
		fmt.Printf("   %s absent — your network resolver will be consulted\n", dnsconf.Path(domain))
		return nil
	}
	fmt.Printf("   removing %s (sudo) so it stops shadowing your network resolver\n", dnsconf.Path(domain))
	return dnsconf.Uninstall(domain)
}

var dnsCmd = &cobra.Command{
	Use:     "dns [dnsmasq|external]",
	GroupID: groupMachine,
	Short:   "Show or switch how *.test resolves",
	Long: `dragonrun can answer *.test itself, or stand aside for a resolver that
already does.

  dnsmasq   run the bundled resolver and write /etc/resolver/<domain>
  external  run no resolver and remove /etc/resolver/<domain>, so an
            AdGuard Home / Pi-hole / router rewrite is actually consulted

Choose external if something on your network already rewrites *.test. Leaving
/etc/resolver in place alongside it is the failure case: the file wins, and if
the local resolver is down the names simply stop resolving.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := load()
		if err != nil {
			return err
		}

		if len(args) == 0 {
			fmt.Printf("mode      %s\n", c.DNS)
			fmt.Printf("domain    *.%s\n", c.Domain)
			if dnsconf.Supported() {
				state := "absent"
				if dnsconf.Installed(c.Domain, c.Ports.DNS) {
					state = fmt.Sprintf("present, pointing at 127.0.0.1:%d", c.Ports.DNS)
				} else if _, err := os.Stat(dnsconf.Path(c.Domain)); err == nil {
					state = "present but STALE (does not match this config)"
				}
				fmt.Printf("resolver  %s — %s\n", dnsconf.Path(c.Domain), state)
			}
			probe := "probe." + c.Domain
			addrs := dnsconf.Addrs(probe)
			if len(addrs) == 0 {
				fmt.Printf("resolves  *.%s -> nothing (probed %s)\n", c.Domain, probe)
			} else {
				fmt.Printf("resolves  *.%s -> %s (probed %s)\n", c.Domain, strings.Join(addrs, ", "), probe)
			}
			fmt.Printf("edge      %s:%d, %s:%d\n", c.Bind, c.Ports.HTTP, c.Bind, c.Ports.HTTPS)
			return nil
		}

		mode := args[0]
		if mode != registry.DNSDnsmasq && mode != registry.DNSExternal {
			return fmt.Errorf("mode must be %q or %q", registry.DNSDnsmasq, registry.DNSExternal)
		}
		if mode == c.DNS {
			fmt.Printf("already in %s mode\n", mode)
			return nil
		}

		c.DNS = mode
		if err := c.Save(); err != nil {
			return err
		}
		stack.SetDNSMode(mode)

		switch mode {
		case registry.DNSExternal:
			if err := ensureNoResolverFile(c.Domain); err != nil {
				return err
			}
			// Bring the stack down BEFORE flipping the profile off, otherwise
			// the dnsmasq container is orphaned: compose would no longer know
			// about it, and it would keep holding the DNS port.
			fmt.Println("stopping the bundled resolver")
			stack.SetDNSMode(registry.DNSDnsmasq)
			if err := stack.Compose("rm", "-sf", "dnsmasq"); err != nil {
				return err
			}
			stack.SetDNSMode(mode)
			fmt.Printf("\nnow in external mode — your network resolver must answer *.%s\n", c.Domain)
			fmt.Printf("verify:  dig +short anything.%s\n", c.Domain)
			fmt.Printf("         expect an address on THIS machine — 127.0.0.1, or its LAN\n")
			fmt.Printf("         address if your resolver rewrites to that. For the latter the\n")
			fmt.Printf("         edge must be published there too: `dragonrun bind`.\n")
		case registry.DNSDnsmasq:
			if _, err := stack.Extract(); err != nil {
				return err
			}
			if err := stack.WriteEnv(c); err != nil {
				return err
			}
			if err := stack.Compose("up", "-d", "dnsmasq"); err != nil {
				return err
			}
			fmt.Printf("writing %s (sudo)\n", dnsconf.Path(c.Domain))
			if err := dnsconf.Install(c.Domain, c.Ports.DNS); err != nil {
				return err
			}
			fmt.Println("\nnow in dnsmasq mode")
		}
		return nil
	},
}

var dnsYes bool
