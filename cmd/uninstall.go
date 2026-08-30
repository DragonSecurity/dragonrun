package cmd

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"git.dragonsecurity.io/dragonrun/internal/dnsconf"
	"git.dragonsecurity.io/dragonrun/internal/edge"
	"git.dragonsecurity.io/dragonrun/internal/provision"
	"git.dragonsecurity.io/dragonrun/internal/registry"
	"git.dragonsecurity.io/dragonrun/internal/stack"
)

// uninstallCmd is the counterpart to install: it undoes every change dragonrun
// made to this machine.
//
// Distinct from `remove`, which deletes ONE project, and from `down -v`, which
// destroys the data but leaves the resolver file, the trusted CA and
// DRAGONRUN_HOME in place -- a half-removed state that looks installed and is
// not.
var uninstallCmd = &cobra.Command{
	Use:     "destroy",
	GroupID: groupMachine,
	Short:   "Undo init: containers, data, DNS, CA and dragonrun's state",
	Long: `Undoes everything ` + "`dragonrun init`" + ` did:

  stops and deletes the containers AND their volumes (all project data)
  removes /etc/resolver/<domain> if dragonrun wrote it
  untrusts caddy's local CA
  deletes DRAGONRUN_HOME, including the registry and every project password

Your repositories are not touched. Their .env files keep the DSNs they have,
which will point at a stack that no longer exists.

This does NOT remove the dragonrun binary.`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		c, err := load()
		if err != nil {
			return err
		}
		home, err := registry.Home()
		if err != nil {
			return err
		}

		fmt.Println("This will DESTROY:")
		fmt.Printf("  %d project(s) and every database they own\n", len(c.Projects))
		if stack.Running() {
			for _, p := range c.Sorted() {
				dbs, err := provision.Databases(c, p)
				if err != nil {
					continue
				}
				fmt.Printf("      %-18s %s\n", p.Name, strings.Join(dbs, ", "))
			}
		} else {
			for _, p := range c.Sorted() {
				fmt.Printf("      %-18s (stack down — databases not listed)\n", p.Name)
			}
		}
		fmt.Println("  the postgres volume — this is not recoverable")
		fmt.Printf("  %s (registry, passwords, generated caddy sites)\n", home)
		if dnsconf.Supported() {
			if _, err := os.Stat(dnsconf.Path(c.Domain)); err == nil {
				fmt.Printf("  %s\n", dnsconf.Path(c.Domain))
			}
		}
		if !keepCA {
			fmt.Println("  caddy's local CA, untrusted from the system keychain")
		}
		fmt.Println("\nYour repositories are NOT touched — their .env files will point at nothing.")

		if !uninstallYes {
			fmt.Printf("\nType 'destroy' to confirm: ")
			line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
			if strings.TrimSpace(line) != "destroy" {
				return fmt.Errorf("aborted")
			}
		}

		// CA first: it needs the exported root.crt, which lives in the home
		// directory this function is about to delete.
		if !keepCA {
			root := filepath.Join(home, "caddy", "root.crt")
			if _, err := os.Stat(root); err == nil {
				removed, err := edge.UntrustCA(root)
				switch {
				case err != nil:
					fmt.Println("could not untrust the CA:", err)
				case removed:
					fmt.Println("untrusted caddy's local CA")
				default:
					fmt.Println("caddy's CA was not in the keychain")
				}
			}
		}

		fmt.Println("stopping the stack and deleting volumes")
		if err := stack.Compose("down", "-v", "--remove-orphans"); err != nil {
			fmt.Println("compose down reported:", err)
		}

		if dnsconf.Supported() {
			if _, err := os.Stat(dnsconf.Path(c.Domain)); err == nil {
				fmt.Printf("removing %s (sudo)\n", dnsconf.Path(c.Domain))
				if err := dnsconf.Uninstall(c.Domain); err != nil {
					fmt.Println("could not remove the resolver file:", err)
				}
			}
		}

		if err := os.RemoveAll(home); err != nil {
			return err
		}
		fmt.Printf("removed %s\n\ndragonrun is gone from this machine. `dragonrun init` starts fresh.\n", home)
		return nil
	},
}

var (
	uninstallYes bool
	keepCA       bool
)

func init() {
	uninstallCmd.Flags().BoolVarP(&uninstallYes, "yes", "y", false, "skip the confirmation prompt")
	uninstallCmd.Flags().BoolVar(&keepCA, "keep-ca", false, "leave caddy's CA trusted in the keychain")
	root.AddCommand(uninstallCmd)
}
