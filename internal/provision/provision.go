// Package provision creates and removes the postgres objects a project owns.
//
// It shells out to psql inside the container rather than linking a driver:
// docker is a hard requirement anyway, and this keeps the host free of any
// postgres client and dragonrun's go.mod free of a database dependency.
package provision

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"git.dragonsecurity.io/dragonrun/internal/registry"
	"git.dragonsecurity.io/dragonrun/internal/stack"
)

// psql runs a script inside the postgres container as the cluster superuser.
// Connection is over the container's unix socket, so no password is involved.
func psql(c *registry.Config, db, script string, vars ...string) (string, error) {
	dir, err := stack.Dir()
	if err != nil {
		return "", err
	}
	args := []string{
		"compose", "--project-directory", dir,
		"-f", dir + "/docker-compose.yml", "--env-file", dir + "/.env",
		"exec", "-T", "postgres",
		"psql", "-v", "ON_ERROR_STOP=1", "-U", c.Superuser, "-d", db, "-qtA",
	}
	for _, v := range vars {
		args = append(args, "-v", v)
	}
	cmd := exec.Command("docker", args...)
	cmd.Stdin = strings.NewReader(script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("psql on %q failed: %w\n%s", db, err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// EnsureRole makes the login role idempotently and syncs its password and
// CREATEDB bit. CREATEDB is what lets a multi-tenant app create tenant
// databases at runtime; without Tenants set the role explicitly loses it.
func EnsureRole(c *registry.Config, p registry.Project) error {
	createdb := "no"
	if p.Tenants {
		createdb = "yes"
	}
	const script = `
SELECT format('CREATE ROLE %I LOGIN', :'role')
 WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = :'role')
\gexec
SELECT format('ALTER ROLE %I LOGIN PASSWORD %L %s', :'role', :'pw',
              CASE WHEN :'createdb' = 'yes' THEN 'CREATEDB' ELSE 'NOCREATEDB' END)
\gexec
`
	_, err := psql(c, "postgres", script,
		"role="+p.Role, "pw="+p.Password, "createdb="+createdb)
	return err
}

// EnsureDB creates the control database from template1 -- which is what makes
// it inherit the pgbouncer auth hook -- then locks it down.
//
// On a shared cluster the default (PUBLIC may CONNECT) would let every project
// role read every other project's data. Revoking it is the main thing standing
// between "one convenient cluster" and "one shared blast radius".
func EnsureDB(c *registry.Config, p registry.Project) error {
	create := `
SELECT format('CREATE DATABASE %I OWNER %I TEMPLATE template1', :'db', :'role')
 WHERE NOT EXISTS (SELECT 1 FROM pg_database WHERE datname = :'db')
\gexec
`
	if _, err := psql(c, "postgres", create, "db="+p.DB, "role="+p.Role); err != nil {
		return err
	}
	return Harden(c, p)
}

// Harden re-applies connect privileges to the control database and every
// tenant database under the project's prefix. Tenant databases are created by
// the application at runtime, so dragonrun only ever sees them after the fact.
func Harden(c *registry.Config, p registry.Project) error {
	dbs, err := Databases(c, p)
	if err != nil {
		return err
	}
	const script = `
SELECT format('REVOKE CONNECT ON DATABASE %I FROM PUBLIC', :'db')
UNION ALL
SELECT format('GRANT CONNECT ON DATABASE %I TO %I', :'db', :'role')
UNION ALL
-- pgbouncer must reach the database to run auth_query against it.
SELECT format('GRANT CONNECT ON DATABASE %I TO pgbouncer_auth', :'db')
\gexec
`
	for _, db := range dbs {
		if _, err := psql(c, "postgres", script, "db="+db, "role="+p.Role); err != nil {
			return err
		}
	}
	return nil
}

// Databases returns the control database plus every tenant database sharing
// the project's prefix, which together are everything the project owns.
func Databases(c *registry.Config, p registry.Project) ([]string, error) {
	// starts_with(), never LIKE: in a LIKE pattern `_` is a single-character
	// wildcard, so a project whose control database is "dragon" would match
	// "dragonlab" with `dragon_%` and DropDatabases would destroy another
	// project's data.
	const q = `
SELECT datname FROM pg_database
 WHERE datname = :'db' OR starts_with(datname, :'prefix')
 ORDER BY datname;
`
	out, err := psql(c, "postgres", q, "db="+p.DB, "prefix="+p.TenantPrefix())
	if err != nil {
		return nil, err
	}
	var dbs []string
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			dbs = append(dbs, l)
		}
	}
	return dbs, nil
}

// Tenants is Databases minus the control database.
func Tenants(c *registry.Config, p registry.Project) ([]string, error) {
	all, err := Databases(c, p)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, d := range all {
		if d != p.DB {
			out = append(out, d)
		}
	}
	return out, nil
}

// DropOne drops a single database. FORCE terminates live backends, including
// pgbouncer's idle pooled server connections, which otherwise block the drop.
func DropOne(c *registry.Config, db string) error {
	script := "SELECT format('DROP DATABASE IF EXISTS %I WITH (FORCE)', :'db')\n\\gexec\n"
	_, err := psql(c, "postgres", script, "db="+db)
	return err
}

// DropDatabases removes every database the project owns. FORCE terminates
// live backends -- without it a single idle psql session blocks the drop.
func DropDatabases(c *registry.Config, p registry.Project) ([]string, error) {
	dbs, err := Databases(c, p)
	if err != nil {
		return nil, err
	}
	for _, db := range dbs {
		if err := DropOne(c, db); err != nil {
			return nil, err
		}
	}
	return dbs, nil
}

// CreateTenant makes a tenant database owned by the project's role, from
// template1 so it inherits both the pgbouncer auth hook and the isolation
// guard -- exactly as one created by the application at runtime would.
func CreateTenant(c *registry.Config, p registry.Project, db string) error {
	const script = `
SELECT format('CREATE DATABASE %I OWNER %I TEMPLATE template1', :'db', :'role')
 WHERE NOT EXISTS (SELECT 1 FROM pg_database WHERE datname = :'db')
\gexec
`
	if _, err := psql(c, "postgres", script, "db="+db, "role="+p.Role); err != nil {
		return err
	}
	return Harden(c, p)
}

func DropRole(c *registry.Config, p registry.Project) error {
	script := "SELECT format('DROP ROLE IF EXISTS %I', :'role')\n\\gexec\n"
	_, err := psql(c, "postgres", script, "role="+p.Role)
	return err
}

// Psql attaches an interactive shell to a database as the project's own role,
// via pgbouncer, so what you type is subject to the same privileges the app has.
func Psql(c *registry.Config, p registry.Project, db string) error {
	dir, err := stack.Dir()
	if err != nil {
		return err
	}
	cmd := exec.Command("docker", "compose",
		"--project-directory", dir,
		"-f", dir+"/docker-compose.yml", "--env-file", dir+"/.env",
		"exec", "-e", "PGPASSWORD="+p.Password, "-it", "postgres",
		"psql", "-h", "pgbouncer", "-p", "6432", "-U", p.Role, "-d", db)
	cmd.Stdout, cmd.Stderr, cmd.Stdin = os.Stdout, os.Stderr, os.Stdin
	return cmd.Run()
}
