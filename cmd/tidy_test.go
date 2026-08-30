package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func composeFile(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "docker-compose.yml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// Top-level keys after `services:` must not be mistaken for services. Every
// compose file in this fleet declares `volumes:` with entries like
// postgres_data, and counting those as unreplaced services would make tidy
// refuse on every repo.
func TestScanComposeIgnoresTopLevelBlocks(t *testing.T) {
	p := composeFile(t, `name: automechanic

services:
  postgres:
    build:
      context: ./postgres
    ports:
      - "5432:5432"
  pgbouncer:
    build:
      context: pgbouncer
  mailpit:
    image: axllent/mailpit

volumes:
  postgres_data:
  caddy_data:

networks:
  default:
`)
	scan, err := scanCompose(p)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"postgres": true, "pgbouncer": true, "mailpit": true}
	if len(scan.services) != len(want) {
		t.Fatalf("services = %v, want exactly %v", scan.services, want)
	}
	for _, s := range scan.services {
		if !want[s] {
			t.Errorf("unexpected service %q — a volume or network was misread", s)
		}
	}
	if len(scan.contexts) != 2 {
		t.Errorf("contexts = %v, want postgres and pgbouncer (both ./x and x forms)", scan.contexts)
	}
}

// The safety property: a service dragonrun does not provide must be detected,
// so tidy keeps the compose file instead of deleting the project's redis.
func TestClassificationKeepsUnreplacedServices(t *testing.T) {
	cases := []struct {
		service string
		replace bool
	}{
		{"postgres", true}, {"db", true}, {"pgbouncer", true},
		{"pgweb", true}, {"pgadmin", true}, {"adminer", true},
		{"mailpit", true}, {"mailhog", true}, {"mailcrab", true},
		// All of these appear in this fleet and dragonrun runs none of them.
		{"redis", false}, {"keycloak", false}, {"minio", false},
		{"openbao", false}, {"registry", false}, {"api", false},
		{"worker", false}, {"osctrl-tls", false},
	}
	for _, tc := range cases {
		_, got := replacedBy[tc.service]
		if got != tc.replace {
			t.Errorf("replacedBy[%q] = %v, want %v", tc.service, got, tc.replace)
		}
	}
}

// looksLikeStackDir stops tidy deleting a build context that is application
// source rather than stack scaffolding.
func TestLooksLikeStackDir(t *testing.T) {
	mk := func(files ...string) string {
		d := t.TempDir()
		for _, f := range files {
			full := filepath.Join(d, f)
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return d
	}
	cases := []struct {
		name string
		dir  string
		want bool
	}{
		{"postgres scaffolding", mk("Dockerfile", "entrypoint.sh"), true},
		{"pgbouncer scaffolding", mk("Dockerfile", "pgbouncer.ini", "userlist.txt"), true},
		{"no Dockerfile", mk("entrypoint.sh"), false},
		{"contains source", mk("Dockerfile", "main.go"), false},
		{"has a subdirectory", mk("Dockerfile", "internal/thing.sh"), false},
		{"empty", mk(), false},
	}
	for _, tc := range cases {
		if got := looksLikeStackDir(tc.dir); got != tc.want {
			t.Errorf("%s: looksLikeStackDir = %v, want %v", tc.name, got, tc.want)
		}
	}
}
