package services

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"git.dragonsecurity.io/dragonrun/internal/registry"
)

func testConfig() *registry.Config {
	c := &registry.Config{
		Domain: "test",
		Ports:  registry.DefaultPorts(),
		Projects: map[string]registry.Project{
			"eyrie":  {Name: "eyrie", Host: "eyrie.test", Upstream: 3000},
			"ledger": {Name: "ledger", DB: "ledger", NoSite: true},
		},
	}
	if _, err := c.EnsureServiceSecrets(); err != nil {
		panic(err)
	}
	return c
}

func render(t *testing.T) (*registry.Config, string, string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("DRAGONRUN_HOME", dir)
	c := testConfig()
	if err := Render(c); err != nil {
		t.Fatal(err)
	}
	dex, err := os.ReadFile(filepath.Join(dir, "dex", "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	realm, err := os.ReadFile(filepath.Join(dir, "keycloak", "import", "dragonrun-realm.json"))
	if err != nil {
		t.Fatal(err)
	}
	return c, string(dex), string(realm)
}

// Keycloak refuses to start on a malformed import and dex refuses to start on
// malformed YAML, so a generator that emits either turns `dragonrun up` into a
// crash loop with the reason buried in container logs.
func TestRealmImportIsValidJSON(t *testing.T) {
	c, _, realm := render(t)

	var got struct {
		Realm   string `json:"realm"`
		Enabled bool   `json:"enabled"`
		Users   []struct {
			Username    string `json:"username"`
			Credentials []struct {
				Value string `json:"value"`
			} `json:"credentials"`
		} `json:"users"`
		Clients []struct {
			ClientID     string   `json:"clientId"`
			Secret       string   `json:"secret"`
			PublicClient bool     `json:"publicClient"`
			RedirectUris []string `json:"redirectUris"`
		} `json:"clients"`
	}
	if err := json.Unmarshal([]byte(realm), &got); err != nil {
		t.Fatalf("realm import is not valid JSON: %v", err)
	}
	if got.Realm != registry.KeycloakRealm || !got.Enabled {
		t.Errorf("realm = %q enabled=%v", got.Realm, got.Enabled)
	}
	if len(got.Users) != 1 || got.Users[0].Username != registry.KeycloakUser {
		t.Fatalf("expected one user %q, got %+v", registry.KeycloakUser, got.Users)
	}
	if got.Users[0].Credentials[0].Value != registry.KeycloakPassword {
		t.Error("test user has the wrong password")
	}

	// The dex client's secret is what dex authenticates with. If the realm and
	// the dex config ever disagree, federation fails at the token exchange --
	// well past the point anyone is still reading logs.
	byID := map[string]int{}
	for i, cl := range got.Clients {
		byID[cl.ClientID] = i
	}
	dexClient, ok := byID[registry.DexHost]
	if !ok {
		t.Fatalf("realm has no %q client: %+v", registry.DexHost, got.Clients)
	}
	if got.Clients[dexClient].Secret != c.Services.DexKeycloakSecret {
		t.Error("dex client secret does not match the registry")
	}
	app, ok := byID["dragonrun"]
	if !ok {
		t.Fatal("realm has no dragonrun client")
	}
	if !got.Clients[app].PublicClient {
		t.Error("the dragonrun client should be public")
	}
	if !strings.Contains(strings.Join(got.Clients[app].RedirectUris, " "), "https://*.test/*") {
		t.Errorf("dragonrun client redirect URIs = %v", got.Clients[app].RedirectUris)
	}
}

// dex matches redirect URIs exactly, so the confidential client is generated
// from the registry. A database-only project has no hostname to call back to.
func TestDexConfigCarriesEveryServingProject(t *testing.T) {
	c, dex, _ := render(t)

	for _, want := range []string{
		"issuer: https://dex.test",
		"database: dex",
		c.Services.DexDBPassword,
		registry.DexStaticPasswordHash,
		`- "https://eyrie.test/callback"`,
		`- "https://eyrie.test/auth/callback"`,
		"connectors:",
		"issuer: https://auth.test/realms/dragonrun",
		c.Services.DexKeycloakSecret,
	} {
		if !strings.Contains(dex, want) {
			t.Errorf("dex config is missing %q:\n%s", want, dex)
		}
	}
	if strings.Contains(dex, "ledger") {
		t.Error("dex config lists a callback for a database-only project")
	}
}

// Keycloak derives its issuer from the Host header caddy forwards, so a
// non-default https port gives dex one issuer and the browser another. Emitting
// the connector anyway produces a login that redirects to an unpublished port,
// which is far harder to diagnose than its absence.
func TestNoKeycloakConnectorOffTheDefaultPort(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DRAGONRUN_HOME", dir)
	c := testConfig()
	c.Ports.HTTPS = 8443
	if err := Render(c); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "dex", "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "type: oidc") {
		t.Errorf("connector emitted on https port 8443:\n%s", b)
	}
	if !strings.Contains(string(b), "No Keycloak connector") {
		t.Errorf("nothing explains the missing connector:\n%s", b)
	}
	// The static account must survive: it is the whole fallback.
	if !strings.Contains(string(b), "enablePasswordDB: true") {
		t.Error("the local account went with the connector")
	}
}
