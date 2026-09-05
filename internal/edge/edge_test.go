package edge

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"git.dragonsecurity.io/dragonrun/internal/registry"
	"git.dragonsecurity.io/dragonrun/internal/stack"
)

// The leaf lifetime is a per-site setting and the intermediate lifetime is a
// global one, so they are stated in two files. A leaf is truncated to its
// issuer's notAfter, which means a Caddyfile that drifted back to the 7-day
// default would silently shorten every certificate again -- exactly the bug
// these lifetimes were raised to fix.
func TestCaddyfileMatchesIntermediateLifetime(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DRAGONRUN_HOME", dir)
	if _, err := stack.Extract(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "stack", "caddy", "Caddyfile"))
	if err != nil {
		t.Fatal(err)
	}
	want := "intermediate_lifetime " + IntermediateLifetime
	if !strings.Contains(string(b), want) {
		t.Errorf("Caddyfile does not set %q", want)
	}
}

func TestGeneratedSitesCarryCertLifetime(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DRAGONRUN_HOME", dir)

	p := registry.Project{Name: "eyrie", Host: "eyrie.test", Upstream: 3000}
	if err := WriteSite(p); err != nil {
		t.Fatal(err)
	}
	if err := WriteServiceSites(&registry.Config{Domain: "test"}); err != nil {
		t.Fatal(err)
	}

	want := "lifetime " + CertLifetime
	for _, name := range []string{"eyrie.caddy", "_services.caddy"} {
		b, err := os.ReadFile(filepath.Join(dir, "caddy", "sites", name))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), want) {
			t.Errorf("%s does not set %q:\n%s", name, want, b)
		}
	}
}

// anyShorterThan is what decides whether an upgraded stack still needs its
// certificates rotated, so it has to be right about the boundary: a full-length
// certificate must not trigger a rotation on every `up`.
func TestAnyShorterThan(t *testing.T) {
	want := 2160 * time.Hour

	cases := []struct {
		name      string
		lifetimes []time.Duration
		short     bool
	}{
		{"caddy's defaults", []time.Duration{12 * time.Hour, 7 * 24 * time.Hour}, true},
		{"leaf rotated, intermediate not", []time.Duration{want, 7 * 24 * time.Hour}, true},
		{"intermediate rotated, leaf cached", []time.Duration{12 * time.Hour, 8760 * time.Hour}, true},
		{"both at full length", []time.Duration{want, 8760 * time.Hour}, false},
		// Caddy signs a moment after it decides the lifetime, so an exact
		// match comes back a few seconds short. That must not count.
		{"a few seconds under", []time.Duration{want - 30*time.Second}, false},
		{"empty bundle", nil, false},
	}
	for _, tc := range cases {
		var bundle []byte
		for _, d := range tc.lifetimes {
			bundle = append(bundle, testCert(t, d)...)
		}
		if got := anyShorterThan(bundle, want); got != tc.short {
			t.Errorf("anyShorterThan(%s) = %v, want %v", tc.name, got, tc.short)
		}
	}
}

// testCert returns a PEM certificate valid for exactly d from now.
func testCert(t *testing.T, d time.Duration) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		NotBefore:    now,
		NotAfter:     now.Add(d),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}
