// Package services owns the three built-in identity and secrets services --
// Keycloak, OpenBao and dex -- beyond what the compose file can express.
//
// Two of them need a configuration file carrying the domain and generated
// secrets, which an embedded asset cannot provide, so those are RENDERED into
// DRAGONRUN_HOME on every `up` exactly as caddy site files are. All three need
// a postgres role and database created before they will start. OpenBao needs
// one more thing on top: its storage is sealed, and the key that opens it is
// held in registry.json rather than by a human.
package services

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"git.dragonsecurity.io/dragonrun/internal/provision"
	"git.dragonsecurity.io/dragonrun/internal/registry"
	"git.dragonsecurity.io/dragonrun/internal/stack"
)

// EnsureDatabases creates the role and database each service owns. Idempotent,
// and run on every `up` so an install that predates these services picks them
// up without a separate command.
func EnsureDatabases(c *registry.Config) error {
	for _, s := range []struct{ name, pw string }{
		{registry.KeycloakDB, c.Services.KeycloakDBPassword},
		{registry.DexDB, c.Services.DexDBPassword},
		{registry.BaoDB, c.Services.BaoDBPassword},
	} {
		if s.pw == "" {
			return fmt.Errorf("no password generated for %q -- run `dragonrun init`", s.name)
		}
		if err := provision.EnsureServiceDB(c, s.name, s.pw); err != nil {
			return err
		}
	}
	return nil
}

// Render writes the generated configuration the services read. Called before
// every `up`, so a changed domain or a newly registered project reaches them
// without anyone remembering a second command.
func Render(c *registry.Config) error {
	if err := renderRealm(c); err != nil {
		return err
	}
	return renderDex(c)
}

func homeFile(parts ...string) (string, error) {
	h, err := registry.Home()
	if err != nil {
		return "", err
	}
	p := filepath.Join(append([]string{h}, parts...)...)
	return p, os.MkdirAll(filepath.Dir(p), 0o755)
}

// URL is the browser-facing address of a built-in service.
func URL(c *registry.Config, host string) string {
	if c.Ports.HTTPS != 443 {
		return fmt.Sprintf("https://%s.%s:%d", host, c.Domain, c.Ports.HTTPS)
	}
	return fmt.Sprintf("https://%s.%s", host, c.Domain)
}

// Issuer is the OIDC issuer of the imported Keycloak realm -- the value an
// application puts in its own configuration.
func Issuer(c *registry.Config) string {
	return URL(c, registry.KeycloakHost) + "/realms/" + registry.KeycloakRealm
}

// renderRealm writes the realm Keycloak imports on first start.
//
// `--import-realm` is create-only: once the realm exists in postgres this file
// is read and skipped, so editing it does NOT push changes into a running
// Keycloak. That is the right way round -- a realm you have been clicking
// around in all afternoon is not silently reverted by an `up`.
func renderRealm(c *registry.Config) error {
	path, err := homeFile("keycloak", "import", registry.KeycloakRealm+"-realm.json")
	if err != nil {
		return err
	}
	realm := map[string]any{
		"realm":                 registry.KeycloakRealm,
		"enabled":               true,
		"displayName":           "dragonrun",
		"sslRequired":           "none", // the edge terminates TLS; the hop to keycloak is plaintext by design
		"loginWithEmailAllowed": true,
		"registrationAllowed":   false,
		"users": []any{
			map[string]any{
				"username":      registry.KeycloakUser,
				"enabled":       true,
				"emailVerified": true,
				"email":         registry.KeycloakUser + "@" + c.Domain,
				"firstName":     "Dev",
				"lastName":      "User",
				"credentials": []any{map[string]any{
					"type":      "password",
					"value":     registry.KeycloakPassword,
					"temporary": false,
				}},
				"realmRoles": []string{"default-roles-" + registry.KeycloakRealm},
			},
		},
		"clients": []any{
			// The client dex federates through. Confidential, because dex can
			// keep a secret.
			map[string]any{
				"clientId":                  registry.DexHost,
				"name":                      "dex",
				"enabled":                   true,
				"protocol":                  "openid-connect",
				"publicClient":              false,
				"secret":                    c.Services.DexKeycloakSecret,
				"standardFlowEnabled":       true,
				"directAccessGrantsEnabled": true,
				"redirectUris":              []string{URL(c, registry.DexHost) + "/callback"},
				"webOrigins":                []string{"+"},
			},
			// The client an application under development uses directly.
			// Public and wildcarded: Keycloak accepts patterns in a redirect
			// URI, so every *.<domain> site and every localhost port a dev
			// server picks is covered without re-registering anything.
			map[string]any{
				"clientId":                  "dragonrun",
				"name":                      "dragonrun (local development)",
				"enabled":                   true,
				"protocol":                  "openid-connect",
				"publicClient":              true,
				"standardFlowEnabled":       true,
				"directAccessGrantsEnabled": true,
				"redirectUris": []string{
					"https://*." + c.Domain + "/*",
					"http://localhost:*",
					"http://127.0.0.1:*",
				},
				"webOrigins": []string{"+"},
			},
		},
	}
	b, err := json.MarshalIndent(realm, "", "  ")
	if err != nil {
		return err
	}
	// 0644: the container reads it as a different uid. It carries the dex
	// client secret, which is why the directory above it is inside a
	// DRAGONRUN_HOME nothing else shares.
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

// renderDex writes dex's config: postgres storage, a fixed local account, and
// an OIDC connector pointing back at the Keycloak realm.
func renderDex(c *registry.Config) error {
	path, err := homeFile("dex", "config.yaml")
	if err != nil {
		return err
	}
	body := fmt.Sprintf(`# Generated by dragonrun -- rewritten on every `+"`dragonrun up`"+`.
# Edits are lost; change registry.json or the generator instead.

issuer: %[1]s

storage:
  type: postgres
  config:
    # Direct to postgres, not the pooler: dex takes advisory-style session
    # state during migrations and transaction pooling discards it.
    host: postgres
    port: 5432
    database: %[2]s
    user: %[2]s
    password: %[3]q
    ssl:
      mode: disable

web:
  http: 0.0.0.0:5556

oauth2:
  # A local dev IdP asking you to approve the same scopes forty times a day is
  # friction with nothing on the other side of it.
  skipApprovalScreen: true

staticClients:
  # Public client with NO redirectURIs listed: dex then accepts any
  # http://localhost:<port> callback (RFC 8252), which is every dev server on
  # this machine without re-registering one of them.
  - id: dragonrun
    name: dragonrun (local development)
    public: true

  # Confidential client for the things that can hold a secret.
  - id: dragonrun-web
    name: dragonrun (confidential)
    secret: %[4]q
    redirectURIs:
%[5]s
enablePasswordDB: true
staticPasswords:
  # Fixed and public: %[6]s / %[7]s. This is a throwaway account inside a
  # loopback dev stack, and `+"`dragonrun services`"+` prints it -- a generated
  # password here would be a secret whose only job is to be looked up.
  - email: %[6]s@%[8]s
    username: %[6]s
    userID: 08a8684b-db88-4b73-90a9-3cd1661f5466
    hash: %[9]q

%[10]s`,
		URL(c, registry.DexHost),       // 1
		registry.DexDB,                 // 2
		c.Services.DexDBPassword,       // 3
		c.Services.DexClientSecret,     // 4
		dexRedirectURIs(c),             // 5
		registry.DexStaticUser,         // 6
		registry.DexStaticPassword,     // 7
		c.Domain,                       // 8
		registry.DexStaticPasswordHash, // 9
		dexKeycloakConnector(c),        // 10
	)
	return os.WriteFile(path, []byte(body), 0o644)
}

// dexRedirectURIs lists the callbacks the confidential client may use.
//
// dex matches a redirect URI EXACTLY -- no wildcards, unlike Keycloak -- so
// this is generated from the registry and refreshed on every `up`. A project
// registered today is a valid callback tomorrow without touching this file.
// The three paths are the conventions the common OIDC libraries default to.
func dexRedirectURIs(c *registry.Config) string {
	var b strings.Builder
	for _, p := range c.Sorted() {
		if !p.Serves() {
			continue
		}
		for _, path := range []string{"/callback", "/auth/callback", "/oauth2/callback"} {
			fmt.Fprintf(&b, "      - %q\n", "https://"+p.Host+path)
		}
	}
	if b.Len() == 0 {
		// An empty list would make dex accept the localhost forms instead,
		// which is the public client's job and would quietly turn this one
		// into a second copy of it.
		b.WriteString("      - \"http://localhost:8080/callback\"\n")
	}
	return b.String()
}

// dexKeycloakConnector renders the OIDC connector that federates dex to the
// Keycloak realm -- or, when it cannot work, an explanation of why.
//
// It cannot work on a non-default HTTPS port. Keycloak derives its issuer from
// the Host header caddy forwards, so dex reaching the edge inside the compose
// network (where caddy always listens on 443) discovers issuer
// "https://auth.<domain>", while a browser on "https://auth.<domain>:8443"
// gets a different one. dex then redirects the browser to an authorize
// endpoint on a port nothing is published on. A comment in the file beats a
// login loop nobody can explain.
func dexKeycloakConnector(c *registry.Config) string {
	if c.Ports.HTTPS != 443 {
		return fmt.Sprintf(`# No Keycloak connector.
#
# The edge is on https port %d, not 443. dex reaches caddy inside the compose
# network, where it always listens on 443, so Keycloak would advertise
# https://%s.%s as its issuer while a browser is on port %d -- and dex would
# send the browser to an authorize endpoint that is not published. The local
# account above still works.
#
# Move the edge back with: dragonrun init --https-port 443
`, c.Ports.HTTPS, registry.KeycloakHost, c.Domain, c.Ports.HTTPS)
	}
	return fmt.Sprintf(`connectors:
  - type: oidc
    id: keycloak
    name: Keycloak
    config:
      issuer: %s
      clientID: %s
      clientSecret: %q
      redirectURI: %s/callback
      scopes: [openid, profile, email]
      # caddy issues from its own local CA, and dex would have to be handed
      # that root before it starts -- which on a first up does not exist yet.
      # Both ends of this connection are containers on one compose network on
      # a developer's machine, so verifying buys nothing the network boundary
      # is not already providing. Swap for rootCAs: [/path/to/root.crt] if
      # that stops being true.
      insecureSkipVerify: true
      # Keycloak marks an imported user verified; a realm built by hand will
      # not, and dex refuses an unverified email without this.
      insecureSkipEmailVerified: true
`, Issuer(c), registry.DexHost, c.Services.DexKeycloakSecret, URL(c, registry.DexHost))
}

// bao runs the OpenBao CLI inside its own container, as root, and returns
// combined output whatever the exit status -- `bao status` exits 2 when
// sealed, which is a state to read, not an error.
func bao(c *registry.Config, args ...string) (string, error) {
	sub := []string{"exec", "-T", "-e", "BAO_ADDR=http://127.0.0.1:8200"}
	if c.Services.BaoRootToken != "" {
		sub = append(sub, "-e", "BAO_TOKEN="+c.Services.BaoRootToken)
	}
	sub = append(sub, "openbao", "bao")
	full, err := stack.ComposeArgs(append(sub, args...)...)
	if err != nil {
		return "", err
	}
	out, err := exec.Command("docker", full...).CombinedOutput()
	return string(out), err
}

// baoStatus is the subset of `bao status -format=json` that matters here.
type baoStatus struct {
	Initialized bool `json:"initialized"`
	Sealed      bool `json:"sealed"`
}

// EnsureBao brings OpenBao from "container just started" to "unsealed and
// usable", and reports what it had to do.
//
// The seal is the price of persistent storage, and paying it by hand on every
// `dragonrun up` would make the whole thing not worth having. So dragonrun
// holds the single unseal key in registry.json -- the same file that already
// holds every project's database password -- and opens the seal itself.
//
// That is a real trade and worth stating plainly: anyone who can read
// registry.json can read every secret in OpenBao. It buys a local instance
// that behaves like the real thing (mounts, policies and JWT auth roles that
// survive a restart) instead of a dev-mode toy that forgets them.
func EnsureBao(c *registry.Config) (action string, err error) {
	st, err := waitBao(c)
	if err != nil {
		return "", err
	}

	switch {
	case !st.Initialized && c.BaoInitialised():
		// Keys in the registry but empty storage: the database was dropped
		// under a live registry. Re-initialising would work but would silently
		// invalidate the key someone may have written down, so say so instead.
		return "", fmt.Errorf("openbao storage is empty but registry.json still holds an unseal key -- "+
			"the %q database was dropped. Clear bao_unseal_key and bao_root_token from registry.json "+
			"to initialise a fresh one, accepting that anything in the old instance is gone", registry.BaoDB)
	case !st.Initialized:
		if err := initBao(c); err != nil {
			return "", err
		}
		action = "initialised"
	case !c.BaoInitialised():
		return "", fmt.Errorf("openbao is initialised but registry.json holds no unseal key -- "+
			"nothing can open this storage. Restore the registry, or drop the %q database "+
			"to start over", registry.BaoDB)
	}

	if st, err = status(c); err != nil {
		return action, err
	}
	if !st.Sealed {
		if action == "" {
			action = "already unsealed"
		}
		return action, nil
	}
	if out, err := bao(c, "operator", "unseal", c.Services.BaoUnsealKey); err != nil {
		return action, fmt.Errorf("openbao unseal failed: %w\n%s", err, strings.TrimSpace(out))
	}
	if action == "" {
		action = "unsealed"
	}
	return action, nil
}

// initBao runs the one operation that can never be repeated, and records its
// output before doing anything else with it.
//
// One share, one threshold. Splitting the key across five holders is a
// production control; here the only holder is registry.json, and three shares
// in one file is theatre.
func initBao(c *registry.Config) error {
	out, err := bao(c, "operator", "init", "-key-shares=1", "-key-threshold=1", "-format=json")
	if err != nil {
		return fmt.Errorf("openbao init failed: %w\n%s", err, strings.TrimSpace(out))
	}
	var r struct {
		Keys      []string `json:"unseal_keys_b64"`
		RootToken string   `json:"root_token"`
	}
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		return fmt.Errorf("could not read `bao operator init` output: %w\n%s", err, strings.TrimSpace(out))
	}
	if len(r.Keys) == 0 || r.RootToken == "" {
		return fmt.Errorf("`bao operator init` returned no key or token:\n%s", strings.TrimSpace(out))
	}
	c.Services.BaoUnsealKey, c.Services.BaoRootToken = r.Keys[0], r.RootToken
	// Saved BEFORE the unseal that follows. If the process dies in between,
	// the next `up` finds a sealed instance it can still open; the other order
	// loses the only copy of a key that cannot be reissued.
	return c.Save()
}

func status(c *registry.Config) (baoStatus, error) {
	var st baoStatus
	out, err := bao(c, "status", "-format=json")
	// Exit 2 is "sealed", which is exactly what we are asking about, so the
	// JSON is what decides -- not the exit code.
	if jsonErr := json.Unmarshal([]byte(out), &st); jsonErr != nil {
		if err != nil {
			return st, fmt.Errorf("openbao is not answering: %w\n%s", err, strings.TrimSpace(out))
		}
		return st, fmt.Errorf("could not read `bao status`: %w\n%s", jsonErr, strings.TrimSpace(out))
	}
	return st, nil
}

// waitBao polls until the server is listening. `up -d` returns when the
// container has STARTED, and OpenBao's first act is to connect to postgres and
// create its tables, so the first few probes legitimately fail.
func waitBao(c *registry.Config) (baoStatus, error) {
	var last error
	for i := 0; i < 30; i++ {
		st, err := status(c)
		if err == nil {
			return st, nil
		}
		last = err
		time.Sleep(time.Second)
	}
	return baoStatus{}, fmt.Errorf("openbao did not come up: %w", last)
}
