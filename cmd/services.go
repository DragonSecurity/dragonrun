package cmd

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"git.dragonsecurity.io/dragonrun/internal/registry"
	"git.dragonsecurity.io/dragonrun/internal/services"
)

// servicesCmd answers "how do I actually log into this" for the built-in
// identity and secrets services, which is otherwise a hunt through
// registry.json and a generated dex config.
//
// It prints secrets to the terminal. That is the point -- they are the
// credentials of a loopback dev stack and every one of them is already in a
// 0600 file on this machine -- but it is why nothing prints them unasked.
var servicesCmd = &cobra.Command{
	Use:     "services",
	Aliases: []string{"idp"},
	GroupID: groupStack,
	Short:   "URLs, credentials and OIDC endpoints for the built-in services",
	Long: `Everything needed to point an application at the stack's own Keycloak,
OpenBao or dex, including the OpenBao root token.

The Keycloak realm is imported ONCE, on the first start against an empty
database. Changing the generated realm file afterwards does nothing -- edit the
realm in the admin console instead, or drop the keycloak database to reimport.`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		c, err := load()
		if err != nil {
			return err
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)

		fmt.Println("keycloak")
		row(w, "  console\t%s\t(or http://localhost:%d)\n",
			services.URL(c, registry.KeycloakHost), c.Ports.Keycloak)
		row(w, "  admin\t%s / %s\t(master realm)\n", c.Superuser, c.Services.KeycloakAdminPassword)
		row(w, "  realm\t%s\n", registry.KeycloakRealm)
		row(w, "  issuer\t%s\n", services.Issuer(c))
		row(w, "  test user\t%s / %s\n", registry.KeycloakUser, registry.KeycloakPassword)
		row(w, "  client\tdragonrun\t(public; any *.%s or localhost callback)\n", c.Domain)
		if err := w.Flush(); err != nil {
			return err
		}

		w = tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Println("\nopenbao")
		row(w, "  ui\t%s\t(or http://localhost:%d)\n",
			services.URL(c, registry.BaoHost), c.Ports.Bao)
		row(w, "  BAO_ADDR\thttp://localhost:%d\n", c.Ports.Bao)
		if c.BaoInitialised() {
			row(w, "  root token\t%s\n", c.Services.BaoRootToken)
			row(w, "  unseal key\t%s\n", c.Services.BaoUnsealKey)
		} else {
			row(w, "  status\tnot initialised — run `dragonrun up`\n")
		}
		if err := w.Flush(); err != nil {
			return err
		}
		// Said every time, not once in a README: this is the one credential in
		// the stack that cannot be regenerated, and the storage is worthless
		// without it.
		fmt.Println("  NOTE the unseal key exists only in registry.json. Lose it and the")
		fmt.Println("       openbao database can never be opened again.")

		w = tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Println("\ndex")
		row(w, "  issuer\t%s\t(or http://localhost:%d)\n",
			services.URL(c, registry.DexHost), c.Ports.Dex)
		row(w, "  local user\t%s@%s / %s\n",
			registry.DexStaticUser, c.Domain, registry.DexStaticPassword)
		if c.Ports.HTTPS == 443 {
			row(w, "  connector\tkeycloak\t(federates to the %s realm)\n", registry.KeycloakRealm)
		} else {
			row(w, "  connector\tnone\t(needs the edge on https 443 — see the generated dex config)\n")
		}
		row(w, "  client\tdragonrun\t(public; any localhost callback)\n")
		row(w, "  client\tdragonrun-web\t%s\n", c.Services.DexClientSecret)
		if err := w.Flush(); err != nil {
			return err
		}
		fmt.Println("\n  dex matches redirect URIs exactly. dragonrun-web is generated with")
		fmt.Println("  /callback, /auth/callback and /oauth2/callback for every registered")
		fmt.Println("  project, refreshed on `dragonrun up`.")
		return nil
	},
}

func init() { root.AddCommand(servicesCmd) }
