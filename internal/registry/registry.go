// Package registry is dragonrun's only piece of durable state: which projects
// exist, what role and password each owns, and where its HTTP upstream lives.
//
// Everything else -- postgres roles, pgbouncer routing, caddy site files,
// pgweb bookmarks -- is derived from this and can be rebuilt with `dragonrun
// sync`. Treat the JSON file as the source of truth and the stack as a cache.
package registry

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// nameRE is deliberately strict: a project name becomes a postgres role, a
// database name, a DNS label and a filename. The intersection of what those
// four accept is narrower than any one of them.
var nameRE = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)

type Project struct {
	Name     string `json:"name"`
	Role     string `json:"role"`
	Password string `json:"password"`
	DB       string `json:"db"`
	Host     string `json:"host"`
	Upstream int    `json:"upstream"`
	Path     string `json:"path,omitempty"`
	// Tenants grants the role CREATEDB so the app can spin up per-tenant
	// databases at runtime. They are named <db>_<tenant> and dragonrun treats
	// anything matching that prefix as belonging to this project.
	Tenants bool `json:"tenants"`
}

// TenantPrefix is the namespace a project owns in the shared cluster. Two
// projects both creating a "tenant_1" would collide without it.
func (p Project) TenantPrefix() string { return p.DB + "_" }

// Ports is where the stack publishes on the host. The defaults are the
// CANONICAL ones the ~60 per-project compose files already use, so an adopted
// project's existing localhost DSNs land here unchanged. They are overridable
// because a tool whose purpose is ending port collisions must not itself be
// the one thing that cannot move.
type Ports struct {
	Postgres int `json:"postgres"`
	Bouncer  int `json:"bouncer"`
	SMTP     int `json:"smtp"`
	MailUI   int `json:"mail_ui"`
	Pgweb    int `json:"pgweb"`
	HTTP     int `json:"http"`
	HTTPS    int `json:"https"`
	DNS      int `json:"dns"`
}

func DefaultPorts() Ports {
	return Ports{
		Postgres: 5432, Bouncer: 6432,
		SMTP: 1025, MailUI: 8025, Pgweb: 8081,
		HTTP: 80, HTTPS: 443,
		// High by design: /etc/resolver carries a `port` line, so dnsmasq
		// never needs a privileged bind or a fight with anything on 53.
		DNS: 15353,
	}
}

// fill replaces any zero value with its default, so a registry written by an
// older dragonrun gains new ports without manual editing.
func (p *Ports) fill() {
	d := DefaultPorts()
	for _, f := range []struct {
		v   *int
		def int
	}{
		{&p.Postgres, d.Postgres}, {&p.Bouncer, d.Bouncer},
		{&p.SMTP, d.SMTP}, {&p.MailUI, d.MailUI}, {&p.Pgweb, d.Pgweb},
		{&p.HTTP, d.HTTP}, {&p.HTTPS, d.HTTPS}, {&p.DNS, d.DNS},
	} {
		if *f.v == 0 {
			*f.v = f.def
		}
	}
}

// DNS modes. dragonrun can run its own resolver, or stand aside for one that
// already exists on the network.
const (
	// DNSDnsmasq runs the bundled dnsmasq and writes /etc/resolver/<domain>.
	DNSDnsmasq = "dnsmasq"
	// DNSExternal assumes something upstream -- AdGuard Home, Pi-hole, a
	// router -- already answers *.<domain>. dragonrun runs no resolver and
	// writes no resolver file, so the upstream one is actually consulted:
	// /etc/resolver takes precedence over the system resolver, and a stale
	// file silently shadows a perfectly good network-wide rewrite.
	DNSExternal = "external"
)

type Config struct {
	Domain                string `json:"domain"`
	DNS                   string `json:"dns"`
	Ports                 Ports  `json:"ports"`
	Superuser             string `json:"superuser"`
	SuperuserPassword     string `json:"superuser_password"`
	PgbouncerAuthPassword string `json:"pgbouncer_auth_password"`
	// TrustedCAs are SHA-1 fingerprints of every caddy root dragonrun has put
	// in the system keychain. Recreating the caddy volume mints a brand new CA,
	// so without this the old one lingers as trusted-but-useless clutter and a
	// fresh one accumulates on every reset.
	TrustedCAs []string           `json:"trusted_cas,omitempty"`
	Projects   map[string]Project `json:"projects"`
}

// Home is ~/.dragonrun -- generated artefacts plus registry.json.
func Home() (string, error) {
	if v := os.Getenv("DRAGONRUN_HOME"); v != "" {
		return v, nil
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, ".dragonrun"), nil
}

func path() (string, error) {
	h, err := Home()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, "registry.json"), nil
}

// Load returns the registry, or a zero-valued one if dragonrun has never been
// installed. Callers that need a provisioned stack should check Installed.
func Load() (*Config, error) {
	p, err := path()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return &Config{Domain: "test", Superuser: "dragon", DNS: DNSDnsmasq,
			Ports: DefaultPorts(), Projects: map[string]Project{}}, nil
	}
	if err != nil {
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("registry.json is corrupt: %w", err)
	}
	if c.Projects == nil {
		c.Projects = map[string]Project{}
	}
	if c.Domain == "" {
		c.Domain = "test"
	}
	c.Ports.fill()
	if c.DNS == "" {
		c.DNS = DNSDnsmasq
	}
	return &c, nil
}

func (c *Config) Installed() bool { return c.SuperuserPassword != "" }

// Save writes atomically at 0600 -- the file holds every project's database
// password in plaintext, which is acceptable for a local dev tool but is the
// reason this never lands inside a git worktree.
func (c *Config) Save() error {
	p, err := path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

func (c *Config) Get(name string) (Project, bool) {
	p, ok := c.Projects[name]
	return p, ok
}

// Sorted returns projects in a stable order so command output does not shuffle
// between runs.
func (c *Config) Sorted() []Project {
	out := make([]Project, 0, len(c.Projects))
	for _, p := range c.Projects {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// UpstreamTaken reports whether another project already claims a host port,
// which is the exact class of collision dragonrun exists to end.
func (c *Config) UpstreamTaken(port int, except string) (string, bool) {
	for _, p := range c.Projects {
		if p.Name != except && p.Upstream == port {
			return p.Name, true
		}
	}
	return "", false
}

// DBTaken reports whether another project already owns a control database
// name. In a shared cluster this MUST be fatal: two projects pointed at one
// database would silently interleave their schemas and migrations, and the
// second `register` would look like it succeeded.
func (c *Config) DBTaken(db, except string) (string, bool) {
	for _, p := range c.Projects {
		if p.Name == except {
			continue
		}
		// A tenant prefix collision is just as bad: project "dragon" would
		// claim "dragon_acme", which project "dragon-acme" also maps to.
		if p.DB == db || strings.HasPrefix(db, p.TenantPrefix()) || strings.HasPrefix(p.DB, db+"_") {
			return p.Name, true
		}
	}
	return "", false
}

// Reserved names are the built-in service hostnames. A project called "mail"
// would produce a second `mail.test` site block and caddy refuses duplicate
// addresses, taking the whole edge down rather than just that project.
var Reserved = map[string]bool{"mail": true, "pgweb": true, "db": true}

// RecordCA remembers a fingerprint dragonrun trusted, and reports which
// previously-trusted ones are now superseded.
func (c *Config) RecordCA(fp string) []string {
	var superseded []string
	for _, old := range c.TrustedCAs {
		if old != fp {
			superseded = append(superseded, old)
		}
	}
	c.TrustedCAs = []string{fp}
	return superseded
}

func ValidName(s string) error {
	if Reserved[s] {
		return fmt.Errorf("%q is reserved for a built-in dragonrun service — pick another name", s)
	}
	if !nameRE.MatchString(s) {
		return fmt.Errorf("%q: use lowercase letters, digits and single hyphens, starting with a letter", s)
	}
	if len(s) > 40 {
		return fmt.Errorf("%q is too long (max 40)", s)
	}
	return nil
}

// RoleName converts a project name to a postgres identifier. Hyphens are legal
// in a quoted identifier but make every hand-typed psql command need quoting,
// so they become underscores.
func RoleName(name string) string { return strings.ReplaceAll(name, "-", "_") }

func Secret(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	// URL encoding keeps the value safe to drop into a DSN without escaping.
	return strings.TrimRight(base64.URLEncoding.EncodeToString(b), "="), nil
}
