package cmd

import (
	"fmt"
	"io"
	"os"
	"sort"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"git.dragonsecurity.io/dragonrun/internal/provision"
	"git.dragonsecurity.io/dragonrun/internal/stack"
)

// row writes one aligned line. Writes to a tabwriter are buffered, so an error
// here would only ever surface at Flush -- which is where it is checked.
func row(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...)
}

var showCmd = &cobra.Command{
	Use:     "show [name]",
	Aliases: []string{"info"},
	GroupID: groupProject,
	Short:   "Everything about one project: URLs, credentials, databases",
	Long: `Answers "where is this thing and how do I reach it" without you having to
remember a port or open a compose file.

Defaults to the project matching the current directory name.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := load()
		if err != nil {
			return err
		}
		p, err := mustProject(c, args)
		if err != nil {
			return err
		}
		vars := Vars(c, p)

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		row(w, "%s\t%s\n", p.Name, p.Path)
		row(w, "\n")
		row(w, "  site\thttps://%s\t-> host port %d\n", p.Host, p.Upstream)
		row(w, "  mail\thttps://mail.%s\t(or localhost:%d)\n", c.Domain, c.Ports.MailUI)
		row(w, "  pgweb\thttps://pgweb.%s\t(bookmark %q)\n", c.Domain, p.Name)
		row(w, "\n")
		row(w, "  role\t%s\n", p.Role)
		row(w, "  password\t%s\n", p.Password)
		row(w, "  control db\t%s\n", p.DB)
		if p.Tenants {
			row(w, "  tenant dbs\t%s*\t(role has CREATEDB)\n", p.TenantPrefix())
		} else {
			row(w, "  tenant dbs\tno\t(role has NOCREATEDB)\n")
		}
		if err := w.Flush(); err != nil {
			return err
		}

		fmt.Println("\n  environment")
		keys := make([]string, 0, len(vars))
		for k := range vars {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Printf("    %s=%s\n", k, vars[k])
		}

		if !stack.Running() {
			fmt.Println("\n  databases: stack is down — run `dragonrun up`")
			return nil
		}
		dbs, err := provision.Databases(c, p)
		if err != nil {
			return err
		}
		fmt.Printf("\n  databases (%d)\n", len(dbs))
		for _, d := range dbs {
			kind := "tenant"
			if d == p.DB {
				kind = "control"
			}
			fmt.Printf("    %-8s %s\n", kind, d)
		}

		fmt.Printf("\n  psql:  dragonrun psql %s\n", p.Name)
		return nil
	},
}

func init() { root.AddCommand(showCmd) }
