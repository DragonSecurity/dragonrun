package cmd

import (
	"os"

	"git.dragonsecurity.io/dragonrun/internal/registry"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func keysOf(m map[string]string) []string {
	k := make([]string, 0, len(m))
	for x := range m {
		k = append(k, x)
	}
	sort.Strings(k)
	return k
}

func write(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// A .env is often the only copy of a generated secret. The merge must replace
// exactly the managed keys and touch nothing else.
func TestMergePreservesUnmanagedLines(t *testing.T) {
	p := write(t, `# a comment
JWT_SECRET=keep-me
DATABASE_URL=postgres://old@localhost:5432/old

# trailing comment
SMTP_USER=noreply@example.com
`)
	vars := map[string]string{"DATABASE_URL": "postgres://new@db.test:6432/new"}
	if _, err := mergeEnvFile(p, vars, keysOf(vars)); err != nil {
		t.Fatal(err)
	}
	got := read(t, p)
	for _, want := range []string{
		"# a comment",
		"JWT_SECRET=keep-me",
		"DATABASE_URL=postgres://new@db.test:6432/new",
		"# trailing comment",
		"SMTP_USER=noreply@example.com",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "postgres://old") {
		t.Errorf("stale value survived:\n%s", got)
	}
}

// A key that exists only as a commented template line should get its value
// inserted directly beneath it, not appended far away -- and the comment must
// survive, because uncommenting would change a line the user disabled.
func TestMergeAnchorsToCommentedKey(t *testing.T) {
	p := write(t, `# ---- Databases ----
DATABASE_URL=postgres://old@localhost:5432/old
# ADMIN_DATABASE_URL=                 # superuser DSN for tenant create/drop
`)
	vars := map[string]string{
		"DATABASE_URL":       "postgres://new@db.test:6432/new",
		"ADMIN_DATABASE_URL": "postgres://new@db.test:5432/postgres",
	}
	if _, err := mergeEnvFile(p, vars, keysOf(vars)); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(read(t, p)), "\n")

	var commentAt, valueAt = -1, -1
	for i, l := range lines {
		if strings.HasPrefix(l, "# ADMIN_DATABASE_URL=") {
			commentAt = i
		}
		if strings.HasPrefix(l, "ADMIN_DATABASE_URL=") {
			valueAt = i
		}
	}
	if commentAt < 0 {
		t.Fatal("the commented template line was destroyed")
	}
	if valueAt != commentAt+1 {
		t.Errorf("value at %d, want directly after comment at %d:\n%s", valueAt, commentAt, strings.Join(lines, "\n"))
	}
	if strings.Contains(read(t, p), "dragonrun (managed)") {
		t.Error("anchored key should not also be appended")
	}
}

// Running env --write twice must not drift or duplicate.
func TestMergeIsIdempotent(t *testing.T) {
	p := write(t, "FOO=bar\n# DATABASE_URL=  # docs\n")
	vars := map[string]string{"DATABASE_URL": "postgres://x@db.test:6432/x", "BASE_URL": "https://x.test"}
	for i := 0; i < 3; i++ {
		if _, err := mergeEnvFile(p, vars, keysOf(vars)); err != nil {
			t.Fatal(err)
		}
	}
	got := read(t, p)
	for k := range vars {
		n := strings.Count(got, "\n"+k+"=")
		// A key on the very first line has no preceding newline to count.
		if strings.HasPrefix(got, k+"=") {
			n++
		}
		if n != 1 {
			t.Errorf("%s assigned %d times, want 1:\n%s", k, n, got)
		}
	}
}

func TestMergeCreatesMissingFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".env")
	vars := map[string]string{"DATABASE_URL": "postgres://x@db.test:6432/x"}
	if _, err := mergeEnvFile(p, vars, keysOf(vars)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(read(t, p), "DATABASE_URL=postgres://x@db.test:6432/x") {
		t.Errorf("value missing from new file:\n%s", read(t, p))
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v, want 0600 — the file holds database passwords", fi.Mode().Perm())
	}
}

// A live assignment must win over a commented one, or the key would be written
// in both places.
func TestMergePrefersLiveOverCommented(t *testing.T) {
	p := write(t, "# DATABASE_URL=  # docs\nDATABASE_URL=postgres://old@x/y\n")
	vars := map[string]string{"DATABASE_URL": "postgres://new@db.test:6432/new"}
	if _, err := mergeEnvFile(p, vars, keysOf(vars)); err != nil {
		t.Fatal(err)
	}
	got := read(t, p)
	if n := strings.Count(got, "\nDATABASE_URL=") + strings.Count(got, "DATABASE_URL=postgres://new") - 1; n > 1 {
		t.Errorf("assigned more than once:\n%s", got)
	}
	if !strings.Contains(got, "# DATABASE_URL=  # docs") {
		t.Errorf("comment destroyed:\n%s", got)
	}
}

// The data plane must not depend on DNS. In `external` mode every .test lookup
// goes to the network resolver, so a hostname here would take every project
// down whenever the machine is off that network. Only BASE_URL may use it.
func TestDataPlaneDSNsDoNotDependOnDNS(t *testing.T) {
	c := &registry.Config{Domain: "test", Ports: registry.DefaultPorts()}
	p := registry.Project{
		Name: "automechanic", Role: "automechanic", Password: "pw",
		DB: "dragon", Host: "automechanic.test", Upstream: 8181, Tenants: true,
	}
	v := Vars(c, p)

	for _, k := range []string{"DATABASE_URL", "ADMIN_DATABASE_URL", "RIVER_DATABASE_URL", "SMTP_SERVER"} {
		got, ok := v[k]
		if !ok {
			t.Fatalf("%s missing", k)
		}
		if strings.Contains(got, "."+c.Domain) {
			t.Errorf("%s = %q — must not contain a .%s hostname, it would break off-network", k, got, c.Domain)
		}
		if !strings.Contains(got, "localhost") {
			t.Errorf("%s = %q — want localhost", k, got)
		}
	}

	// BASE_URL is the exception: caddy routes on it, so it genuinely needs DNS.
	if want := "https://automechanic.test"; v["BASE_URL"] != want {
		t.Errorf("BASE_URL = %q, want %q", v["BASE_URL"], want)
	}
}
