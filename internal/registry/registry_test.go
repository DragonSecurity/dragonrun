package registry

import "testing"

func TestValidName(t *testing.T) {
	ok := []string{"eyrie", "dragon-invoice", "autoglue-v2", "a1"}
	bad := []string{"", "1eyrie", "Eyrie", "dragon_invoice", "-eyrie", "eyrie-", "dragon--x"}
	for _, s := range ok {
		if err := ValidName(s); err != nil {
			t.Errorf("ValidName(%q) = %v, want nil", s, err)
		}
	}
	for _, s := range bad {
		if err := ValidName(s); err == nil {
			t.Errorf("ValidName(%q) = nil, want error", s)
		}
	}
}

func TestRoleName(t *testing.T) {
	if got := RoleName("dragon-invoice"); got != "dragon_invoice" {
		t.Errorf("RoleName = %q, want dragon_invoice", got)
	}
}

// TestDBTaken guards the rule that makes a shared cluster safe: no two projects
// may own the same control database, and no project may own a name that falls
// inside another's tenant namespace.
func TestDBTaken(t *testing.T) {
	c := &Config{Projects: map[string]Project{
		"saas":   {Name: "saas", DB: "dragon"},
		"waggle": {Name: "waggle", DB: "waggle"},
	}}

	cases := []struct {
		db, except string
		want       bool
		why        string
	}{
		{"dragon", "", true, "exact collision with saas"},
		{"dragon", "saas", false, "re-registering saas itself is fine"},
		{"dragon_acme", "", true, "falls inside saas's tenant namespace"},
		{"waggle_beta", "", true, "falls inside waggle's tenant namespace"},
		// The bug this file exists for: `dragonlab` shares a prefix with
		// `dragon` only under LIKE's `_` wildcard, not under real prefixing.
		{"dragonlab", "", false, "dragonlab is NOT a tenant of dragon"},
		{"dragoncms", "", false, "dragoncms is NOT a tenant of dragon"},
		{"autobot", "", false, "unrelated name"},
	}
	for _, tc := range cases {
		if _, got := c.DBTaken(tc.db, tc.except); got != tc.want {
			t.Errorf("DBTaken(%q, %q) = %v, want %v — %s", tc.db, tc.except, got, tc.want, tc.why)
		}
	}
}

func TestTenantPrefix(t *testing.T) {
	p := Project{DB: "dragon"}
	if got := p.TenantPrefix(); got != "dragon_" {
		t.Errorf("TenantPrefix = %q, want dragon_", got)
	}
}

func TestPortsFillKeepsExplicitValues(t *testing.T) {
	p := Ports{Postgres: 45432}
	p.fill()
	if p.Postgres != 45432 {
		t.Errorf("fill overwrote an explicit port: %d", p.Postgres)
	}
	if p.Bouncer != DefaultPorts().Bouncer {
		t.Errorf("fill did not supply a missing port: %d", p.Bouncer)
	}
}
