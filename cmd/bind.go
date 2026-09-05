package cmd

import (
	"fmt"
	"net"
	"strings"

	"github.com/spf13/cobra"

	"git.dragonsecurity.io/dragonrun/internal/edge"
	"git.dragonsecurity.io/dragonrun/internal/stack"
)

// hostAddrs lists this machine's own non-loopback addresses, so `bind` can
// suggest the one to use instead of making you go and read ifconfig.
func hostAddrs() []string {
	var out []string
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	for _, a := range addrs {
		n, ok := a.(*net.IPNet)
		if !ok || n.IP.IsLoopback() || n.IP.To4() == nil {
			continue
		}
		out = append(out, n.IP.String())
	}
	return out
}

// validBind accepts an address this machine can actually publish on. Docker
// fails at `up` time with a bind error that says nothing about which of the
// eight published ports was wrong, so it is worth catching here.
func validBind(addr string) error {
	if addr == "0.0.0.0" {
		return nil
	}
	ip := net.ParseIP(addr)
	if ip == nil {
		return fmt.Errorf("%q is not an IP address — pass 127.0.0.1, 0.0.0.0, "+
			"or one of this machine's addresses (%s)", addr, strings.Join(hostAddrs(), ", "))
	}
	if ip.IsLoopback() {
		return nil
	}
	for _, a := range hostAddrs() {
		if a == addr {
			return nil
		}
	}
	return fmt.Errorf("%s is not an address on this machine — docker would refuse to bind it "+
		"(available: %s)", addr, strings.Join(hostAddrs(), ", "))
}

var bindCmd = &cobra.Command{
	Use:     "bind [address]",
	GroupID: groupStack,
	Short:   "Show or change the address the edge publishes on",
	Long: `The caddy edge publishes on 127.0.0.1 by default, so https://<name>.test
works on this machine and nowhere else.

That stops working the moment something else answers the domain. A network-wide
rewrite -- AdGuard Home, Pi-hole, a router -- normally points *.test at this
machine's LAN address so phones and other machines resolve it too, and then a
loopback-only edge is unreachable from everywhere INCLUDING here: the name no
longer resolves to 127.0.0.1.

  dragonrun bind 0.0.0.0        publish on every interface
  dragonrun bind 192.168.1.20   publish on one address only
  dragonrun bind 127.0.0.1      back to loopback

Only the edge moves. Postgres, pgbouncer, mailpit and pgweb stay on loopback
whatever this is set to.

Widening this is a real exposure: anything that can reach the machine and
resolve the domain gets every registered project, plus mail.<domain> and
pgweb.<domain> -- and pgweb connects as the cluster superuser. Loopback on
untrusted networks.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := load()
		if err != nil {
			return err
		}

		if len(args) == 0 {
			fmt.Printf("edge      %s:%d, %s:%d\n", c.Bind, c.Ports.HTTP, c.Bind, c.Ports.HTTPS)
			if c.LANBound() {
				fmt.Println("reach     this machine and anything that can route to it")
			} else {
				fmt.Println("reach     this machine only")
				if addrs := hostAddrs(); len(addrs) > 0 {
					fmt.Printf("available %s\n", strings.Join(addrs, ", "))
				}
			}
			fmt.Println("rest      127.0.0.1 (postgres, pgbouncer, mailpit, pgweb — not movable)")
			return nil
		}

		addr := args[0]
		if err := validBind(addr); err != nil {
			return err
		}
		if addr == c.Bind {
			fmt.Printf("already bound to %s\n", addr)
			return nil
		}

		c.Bind = addr
		if err := c.Save(); err != nil {
			return err
		}
		if !stack.Running() {
			fmt.Printf("edge will publish on %s at the next `dragonrun up`\n", addr)
			return nil
		}
		if _, err := stack.Extract(); err != nil {
			return err
		}
		if err := stack.WriteEnv(c); err != nil {
			return err
		}
		// A published port only changes when the container is recreated; a
		// reload or restart keeps the old mapping and looks like a no-op.
		if err := stack.Compose("up", "-d", "--force-recreate", "caddy"); err != nil {
			return err
		}

		fmt.Printf("\nedge now publishes on %s:%d and %s:%d\n", addr, c.Ports.HTTP, addr, c.Ports.HTTPS)
		if c.LANBound() {
			fmt.Printf("every registered project — and mail.%s and pgweb.%s — is now reachable\n",
				c.Domain, c.Domain)
			fmt.Println("from anything that can route to this machine and resolve the name.")
			// The other device sees the same self-signed CA this machine
			// already trusts, so hand over the exact file rather than leaving
			// someone to guess why their phone shows a certificate warning.
			if path, err := edge.RootCA(); err == nil {
				fmt.Printf("\nother devices need dragonrun's CA installed to trust the certificates:\n")
				fmt.Printf("  %s\n", path)
			}
		}
		return nil
	},
}

func init() { root.AddCommand(bindCmd) }
