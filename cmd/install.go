package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"git.dragonsecurity.io/dragonrun/internal/dnsconf"
	"git.dragonsecurity.io/dragonrun/internal/edge"
	"git.dragonsecurity.io/dragonrun/internal/registry"
	"git.dragonsecurity.io/dragonrun/internal/stack"
)

var installCmd = &cobra.Command{
	Use:     "init",
	GroupID: groupMachine,
	Short:   "Prepare this machine: stack, DNS and local CA (run once)",
	Long: `Generates cluster secrets, brings the stack up, points *.test at
127.0.0.1 via /etc/resolver, and trusts caddy's local CA.

Needs sudo twice: once to write /etc/resolver/test, once to add the CA to the
system keychain. Nothing else touches the system.

This does NOT install the dragonrun binary -- use
` + "`go install git.dragonsecurity.io/dragonrun@latest`" + ` for that.`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if err := stack.RequireDocker(); err != nil {
			return err
		}
		c, err := registry.Load()
		if err != nil {
			return err
		}

		// Secrets are generated once and reused. Regenerating them would
		// orphan every existing project password in the cluster.
		if c.SuperuserPassword == "" {
			if c.SuperuserPassword, err = registry.Secret(24); err != nil {
				return err
			}
		}
		if c.PgbouncerAuthPassword == "" {
			if c.PgbouncerAuthPassword, err = registry.Secret(24); err != nil {
				return err
			}
		}
		if c.Superuser == "" {
			c.Superuser = "dragon"
		}
		if cmd.Flags().Changed("dns") {
			mode, err := cmd.Flags().GetString("dns")
			if err != nil {
				return err
			}
			if mode != registry.DNSDnsmasq && mode != registry.DNSExternal {
				return fmt.Errorf("--dns must be %q or %q", registry.DNSDnsmasq, registry.DNSExternal)
			}
			c.DNS = mode
		}
		if c.DNS == "" {
			c.DNS = registry.DNSDnsmasq
		}
		stack.SetDNSMode(c.DNS)
		for _, f := range []struct {
			name string
			dst  *int
		}{
			{"postgres-port", &c.Ports.Postgres}, {"bouncer-port", &c.Ports.Bouncer},
			{"smtp-port", &c.Ports.SMTP}, {"mail-ui-port", &c.Ports.MailUI},
			{"pgweb-port", &c.Ports.Pgweb}, {"http-port", &c.Ports.HTTP},
			{"https-port", &c.Ports.HTTPS}, {"dns-port", &c.Ports.DNS},
		} {
			if cmd.Flags().Changed(f.name) {
				v, err := cmd.Flags().GetInt(f.name)
				if err != nil {
					return err
				}
				*f.dst = v
			}
		}
		if err := c.Save(); err != nil {
			return err
		}

		dir, err := stack.Extract()
		if err != nil {
			return err
		}
		if err := stack.WriteEnv(c); err != nil {
			return err
		}
		fmt.Println("stack:", dir)

		if err := edge.WriteServiceSites(c); err != nil {
			return err
		}
		if err := edge.WriteAllSites(c); err != nil {
			return err
		}

		fmt.Println("\n== building and starting the stack ==")
		if err := stack.Compose("up", "-d", "--build"); err != nil {
			return err
		}
		if rotated, err := edge.EnsureCertLifetimes(); err != nil {
			return err
		} else if rotated {
			fmt.Println("   rotated the local CA intermediate — certificates now last 90 days")
		}

		// A superuser bookmark for the whole cluster. pgweb's database
		// switcher does the rest, including tenant databases created later.
		h, _ := registry.Home()
		if err := os.MkdirAll(filepath.Join(h, "pgweb", "bookmarks"), 0o755); err != nil {
			return err
		}
		if err := edge.WriteBookmark(c, registry.Project{
			Name: "cluster", DB: "postgres",
		}); err != nil {
			return err
		}

		fmt.Println("\n== DNS ==")
		if c.DNS == registry.DNSExternal {
			fmt.Printf("   external — expecting your network resolver to answer *.%s\n", c.Domain)
			if err := ensureNoResolverFile(c.Domain); err != nil {
				return err
			}
		} else if dnsconf.Installed(c.Domain, c.Ports.DNS) {
			fmt.Printf("   /etc/resolver/%s already correct\n", c.Domain)
		} else if noDNS {
			fmt.Println("   skipped (--no-dns)")
		} else {
			fmt.Printf("   writing /etc/resolver/%s (sudo)\n", c.Domain)
			if err := dnsconf.Install(c.Domain, c.Ports.DNS); err != nil {
				return err
			}
		}

		fmt.Println("\n== local CA ==")
		if noTrust {
			fmt.Println("   skipped (--no-trust); https://*.test will warn until trusted")
		} else if ca, err := edge.RootCA(); err != nil {
			fmt.Println("   deferred:", err)
			fmt.Println("   run `dragonrun trust` once a site has been registered")
		} else {
			fmt.Println("   trusting", ca, "(sudo)")
			if err := edge.TrustCA(ca); err != nil {
				fmt.Println("   could not trust automatically:", err)
			}
		}

		fmt.Printf(`
dragonrun is up.

  mail    https://mail.%[1]s      (or http://localhost:%[2]d)
  pgweb   https://pgweb.%[1]s     (or http://localhost:%[3]d)
  db      localhost:%[4]d pooled  /  localhost:%[5]d direct

Next: cd into a project and run `+"`dragonrun adopt`"+`.
`, c.Domain, c.Ports.MailUI, c.Ports.Pgweb, c.Ports.Bouncer, c.Ports.Postgres)
		return nil
	},
}

var trustCmd = &cobra.Command{
	Use:     "trust",
	GroupID: groupMachine,
	Short:   "Trust caddy's local CA so https://*.test stops warning",
	Long: `Installs caddy's current local CA into the system keychain.

Recreating the caddy volume -- ` + "`down -v`" + `, ` + "`destroy`" + `, or a wiped docker --
mints a brand new CA. The old one stays trusted but matches nothing, so they
accumulate. --prune removes superseded ones.`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		c, err := load()
		if err != nil {
			return err
		}
		ca, err := edge.RootCA()
		if err != nil {
			return err
		}
		fp, err := edge.Fingerprint(ca)
		if err != nil {
			return err
		}

		inKeychain, _ := edge.KeychainCAs()
		already := false
		for _, k := range inKeychain {
			if k == fp {
				already = true
			}
		}
		if already {
			fmt.Println("caddy's current CA is already trusted")
		} else {
			fmt.Println("trusting", ca, "(sudo)")
			if err := edge.TrustCA(ca); err != nil {
				return err
			}
		}

		// Anything else under this name is a caddy root that no longer matches
		// what caddy serves -- almost always one dragonrun trusted before the
		// volume was recreated.
		var stale []string
		for _, k := range inKeychain {
			if k != fp {
				stale = append(stale, k)
			}
		}
		superseded := c.RecordCA(fp)
		if err := c.Save(); err != nil {
			return err
		}

		if len(stale) == 0 {
			return nil
		}
		if !trustPrune {
			fmt.Printf("\n%d superseded caddy CA(s) still trusted:\n", len(stale))
			for _, k := range stale {
				tag := ""
				for _, s := range superseded {
					if s == k {
						tag = "  (trusted by dragonrun)"
					}
				}
				fmt.Printf("  %s%s\n", k, tag)
			}
			fmt.Println("\nremove them with:  dragonrun trust --prune")
			return nil
		}
		fmt.Printf("\npruning %d superseded CA(s) (sudo)\n", len(stale))
		for _, k := range stale {
			if err := edge.DeleteCA(k); err != nil {
				fmt.Printf("  could not remove %s: %v\n", k, err)
				continue
			}
			fmt.Println("  removed", k)
		}
		return nil
	},
}

var trustPrune bool

var noDNS, noTrust bool

func init() {
	d := registry.DefaultPorts()
	installCmd.Flags().Int("postgres-port", d.Postgres, "host port for postgres (direct)")
	installCmd.Flags().Int("bouncer-port", d.Bouncer, "host port for pgbouncer (pooled)")
	installCmd.Flags().Int("smtp-port", d.SMTP, "host port for mailpit SMTP")
	installCmd.Flags().Int("mail-ui-port", d.MailUI, "host port for the mailpit UI")
	installCmd.Flags().Int("pgweb-port", d.Pgweb, "host port for pgweb")
	installCmd.Flags().Int("http-port", d.HTTP, "host port for caddy http")
	installCmd.Flags().Int("https-port", d.HTTPS, "host port for caddy https")
	installCmd.Flags().Int("dns-port", d.DNS, "host port for dnsmasq")
	installCmd.Flags().String("dns", registry.DNSDnsmasq,
		"dnsmasq (run our own resolver) or external (AdGuard/Pi-hole already answers *.test)")
	installCmd.Flags().BoolVar(&noDNS, "no-dns", false, "skip writing /etc/resolver")
	installCmd.Flags().BoolVar(&noTrust, "no-trust", false, "skip trusting the local CA")
	trustCmd.Flags().BoolVar(&trustPrune, "prune", false,
		"also remove caddy CAs that no longer match what caddy serves")
	dnsCmd.Flags().BoolVar(&dnsYes, "yes", false, "skip the confirmation prompt")
	root.AddCommand(installCmd, trustCmd, dnsCmd, legacyInstallCmd, legacyUninstallCmd)
}
