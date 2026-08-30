package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// The machine-level pair used to be called install/uninstall, which reads as
// binary management -- `go install` territory -- and made `dragonrun uninstall`
// look like "remove the tool" when it in fact destroys every database.
//
// Rather than let those names resolve to anything, they are kept as hidden
// commands that explain and refuse. A wrong guess about a destructive command
// must fail loudly, not do something plausible.

var legacyInstallCmd = &cobra.Command{
	Use:    "install",
	Hidden: true,
	Args:   cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return fmt.Errorf(`"install" is not a dragonrun command.

  to prepare this machine (stack, DNS, local CA):
      dragonrun init

  to install the dragonrun binary:
      go install git.dragonsecurity.io/dragonrun@latest`)
	},
}

var legacyUninstallCmd = &cobra.Command{
	Use:    "uninstall",
	Hidden: true,
	Args:   cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return fmt.Errorf(`"uninstall" is not a dragonrun command.

  to remove a single project:
      dragonrun delete <name> --data

  to undo init entirely -- containers, ALL DATA, DNS and the local CA:
      dragonrun destroy

  to remove the dragonrun binary:
      rm $(command -v dragonrun)`)
	},
}
