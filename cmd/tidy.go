package cmd

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

// replacedBy names the services dragonrun provides centrally. Anything not on
// this list is something dragonrun does NOT run -- redis, keycloak, minio,
// openbao and friends appear across this fleet -- and its compose file has to
// stay.
var replacedBy = map[string]string{
	"postgres": "postgres", "postgresql": "postgres", "db": "postgres", "database": "postgres",
	"pgbouncer": "pgbouncer",
	"pgweb":     "pgweb", "pgadmin": "pgweb", "adminer": "pgweb",
	"mailpit": "mailpit", "mailhog": "mailpit", "mailcrab": "mailpit",
}

var (
	serviceRE = regexp.MustCompile(`^  ([a-zA-Z0-9_.-]+):\s*$`)
	contextRE = regexp.MustCompile(`^\s*context:\s*\.?/?([a-zA-Z0-9_.-]+)\s*$`)
	topRE     = regexp.MustCompile(`^[a-zA-Z]`)
)

type composeScan struct {
	services []string
	contexts []string
}

// scanCompose reads the service names and build contexts out of a compose file
// without pulling in a YAML dependency: only the two-space-indented keys under
// a top-level `services:` count as services, which is the shape every file in
// this fleet uses.
func scanCompose(path string) (composeScan, error) {
	var out composeScan
	f, err := os.Open(path)
	if err != nil {
		return out, err
	}
	defer f.Close()

	inServices := false
	seen := map[string]bool{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "services:") {
			inServices = true
			continue
		}
		// A new top-level key (volumes:, networks:, name:) ends the block.
		if inServices && topRE.MatchString(line) {
			inServices = false
		}
		if !inServices {
			continue
		}
		if m := serviceRE.FindStringSubmatch(line); m != nil {
			out.services = append(out.services, m[1])
			continue
		}
		if m := contextRE.FindStringSubmatch(line); m != nil && !seen[m[1]] {
			seen[m[1]] = true
			out.contexts = append(out.contexts, m[1])
		}
	}
	return out, sc.Err()
}

var tidyCmd = &cobra.Command{
	Use:     "tidy [path]",
	GroupID: groupProject,
	Short:   "Remove the per-project stack files dragonrun has made redundant",
	Long: `Once a project is adopted, its docker-compose.yml, postgres/ and pgbouncer/
directories are dead weight. This works out whether they are safe to delete.

Safe means every service in the compose file is one dragonrun provides. Repos
here also run redis, keycloak, minio and openbao — dragonrun does not replace
those, so their compose file stays and tidy removes nothing.

Reports only, unless you pass --apply.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := load()
		if err != nil {
			return err
		}
		dir := ""
		if len(args) > 0 {
			if dir, err = filepath.Abs(args[0]); err != nil {
				return err
			}
		} else if dir, err = os.Getwd(); err != nil {
			return err
		}

		// Refuse on an unadopted repo: deleting its stack before dragonrun can
		// serve it would leave the project with no database at all.
		name := filepath_Base(dir)
		p, ok := c.Get(name)
		if !ok {
			return fmt.Errorf("%q is not registered — run `dragonrun adopt` first, "+
				"otherwise removing its stack would leave it with no database", name)
		}

		composePath := ""
		for _, f := range []string{"docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml"} {
			if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
				composePath = filepath.Join(dir, f)
				break
			}
		}
		if composePath == "" {
			fmt.Printf("%s: no compose file — already tidy\n", p.Name)
			return nil
		}

		scan, err := scanCompose(composePath)
		if err != nil {
			return err
		}

		fmt.Printf("%s  (%s)\n\n  services in %s\n", p.Name, dir, filepath.Base(composePath))
		var keep []string
		for _, s := range scan.services {
			if by, ok := replacedBy[strings.ToLower(s)]; ok {
				fmt.Printf("    %-14s replaced by dragonrun's %s\n", s, by)
			} else {
				fmt.Printf("    %-14s NOT replaced — dragonrun does not run this\n", s)
				keep = append(keep, s)
			}
		}

		if len(keep) > 0 {
			sort.Strings(keep)
			fmt.Printf("\n  → keeping %s: it still runs %s\n",
				filepath.Base(composePath), strings.Join(keep, ", "))
			fmt.Printf("    Trim the replaced services out by hand, then re-run tidy.\n")
			return nil
		}

		// Only build contexts this compose actually referenced, and only ones
		// that look like stack scaffolding, are candidates.
		victims := []string{composePath}
		for _, ctx := range scan.contexts {
			cd := filepath.Join(dir, ctx)
			if fi, err := os.Stat(cd); err != nil || !fi.IsDir() {
				continue
			}
			if !looksLikeStackDir(cd) {
				fmt.Printf("\n  note: %s/ is a build context but does not look like stack scaffolding — leaving it\n", ctx)
				continue
			}
			victims = append(victims, cd)
		}

		fmt.Printf("\n  redundant\n")
		for _, v := range victims {
			rel, _ := filepath.Rel(dir, v)
			if fi, err := os.Stat(v); err == nil && fi.IsDir() {
				entries, _ := os.ReadDir(v)
				var names []string
				for _, e := range entries {
					names = append(names, e.Name())
				}
				fmt.Printf("    %-14s (%s)\n", rel+"/", strings.Join(names, ", "))
			} else {
				fmt.Printf("    %s\n", rel)
			}
		}

		if !tidyApply {
			fmt.Printf("\n  → safe to remove. Re-run with --apply to delete.\n")
			return nil
		}
		for _, v := range victims {
			if err := os.RemoveAll(v); err != nil {
				return err
			}
			rel, _ := filepath.Rel(dir, v)
			fmt.Printf("  removed %s\n", rel)
		}
		fmt.Printf("\n%s no longer carries a stack. `dragonrun up` serves it.\n", p.Name)
		return nil
	},
}

// looksLikeStackDir guards against deleting a build context that happens to be
// application source. Stack scaffolding in these repos is a Dockerfile plus an
// entrypoint or an ini file, and nothing else.
func looksLikeStackDir(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) == 0 || len(entries) > 6 {
		return false
	}
	hasDockerfile := false
	for _, e := range entries {
		if e.IsDir() {
			return false
		}
		n := strings.ToLower(e.Name())
		switch {
		case n == "dockerfile":
			hasDockerfile = true
		case strings.HasSuffix(n, ".sh"), strings.HasSuffix(n, ".ini"),
			strings.HasSuffix(n, ".txt"), strings.HasSuffix(n, ".conf"),
			strings.HasSuffix(n, ".sql"):
		default:
			return false // something unexpected: do not touch this directory
		}
	}
	return hasDockerfile
}

var tidyApply bool

func init() {
	tidyCmd.Flags().BoolVar(&tidyApply, "apply", false, "actually delete the files")
	root.AddCommand(tidyCmd)
}
