package registry

import (
	"encoding/json"
	"reflect"
)

// Built-in service identities. These are the hostnames caddy serves, the
// postgres roles and databases the services own, and the names no project may
// take -- all derived from one list so they cannot drift apart.
const (
	// KeycloakHost etc. are the labels under the configured domain, so with
	// the default domain they are auth.test, bao.test and dex.test.
	KeycloakHost = "auth"
	BaoHost      = "bao"
	DexHost      = "dex"

	// KeycloakDB and DexDB are roles AND databases in the shared cluster. Each
	// role owns its own database and nothing else, so the login guard in
	// template1 keeps them out of every project's data exactly as it keeps
	// projects out of each other's.
	KeycloakDB = "keycloak"
	DexDB      = "dex"
	// OpenBao stores its (already encrypted) blobs in postgres too, rather
	// than a file volume, so all three services share one backend and one
	// answer to "where is the data". OpenBao creates its own tables on first
	// unseal; dragonrun only has to supply the role and the database.
	BaoDB = "openbao"

	// DexStaticUser is the throwaway local account dex offers alongside the
	// Keycloak connector, for logging in when Keycloak is not the thing under
	// test. The password is FIXED and public -- see DexStaticPasswordHash.
	DexStaticUser     = "dev"
	DexStaticPassword = "dev"
	// bcrypt of DexStaticPassword. dex takes a hash, not a password, and
	// hashing one at runtime would mean a bcrypt dependency for a credential
	// that is deliberately not a secret: it exists only inside a loopback dev
	// stack, and printing it in `dragonrun services` is the point.
	DexStaticPasswordHash = `$2y$10$gpprrZqcbi0teAGJUKOBK.ctd.IhQdxzYHkDR8VqP.m2PdiIF7quK`

	// KeycloakRealm is the realm dragonrun imports on first start. The master
	// realm is left alone: it is the admin realm, and putting application
	// clients in it is how a dev setup becomes impossible to reason about.
	KeycloakRealm = "dragonrun"
	// KeycloakUser is the realm's test account, same fixed credential logic
	// as the dex one above.
	KeycloakUser     = "dev"
	KeycloakPassword = "dev"
)

// ServiceDBs are the databases the built-in services own. Nothing in the
// registry accounts for them, so both `DBTaken` and the orphan report have to
// know about them -- otherwise a project could claim `keycloak` as its control
// database, or `dragonrun status` would report the stack's own storage as
// something nobody remembers creating.
var ServiceDBs = map[string]bool{KeycloakDB: true, DexDB: true, BaoDB: true}

// Services holds the credentials for the built-in identity and secrets
// services. They live in registry.json for the same reason the project
// passwords do: this is the only durable state, and regenerating them would
// orphan a Keycloak database or -- for OpenBao -- an unseal key that is the
// ONLY thing standing between the stack and its own storage.
type Services struct {
	KeycloakAdminPassword string `json:"keycloak_admin_password,omitempty"`
	KeycloakDBPassword    string `json:"keycloak_db_password,omitempty"`
	DexDBPassword         string `json:"dex_db_password,omitempty"`
	BaoDBPassword         string `json:"bao_db_password,omitempty"`
	// DexClientSecret is for the `dragonrun` static OIDC client an application
	// under development points at dex with.
	DexClientSecret string `json:"dex_client_secret,omitempty"`
	// DexKeycloakSecret is the other direction: the secret of the `dex` client
	// inside the Keycloak realm, which dex uses to federate.
	DexKeycloakSecret string `json:"dex_keycloak_secret,omitempty"`
	// BaoUnsealKey and BaoRootToken come from `bao operator init`, which can
	// only ever be run once against a storage backend. Lose these and the
	// OpenBao volume is unrecoverable -- there is no reset that keeps the data.
	BaoUnsealKey string `json:"bao_unseal_key,omitempty"`
	BaoRootToken string `json:"bao_root_token,omitempty"`

	extra preserve
}

// EnsureServiceSecrets fills in anything not generated yet and reports whether
// it changed the config. Existing values are never regenerated: the Keycloak
// database password is baked into a running container's config, and the
// OpenBao pair cannot be reissued at all.
func (c *Config) EnsureServiceSecrets() (bool, error) {
	changed := false
	for _, dst := range []*string{
		&c.Services.KeycloakAdminPassword,
		&c.Services.KeycloakDBPassword,
		&c.Services.DexDBPassword,
		&c.Services.BaoDBPassword,
		&c.Services.DexClientSecret,
		&c.Services.DexKeycloakSecret,
	} {
		if *dst != "" {
			continue
		}
		s, err := Secret(24)
		if err != nil {
			return changed, err
		}
		*dst = s
		changed = true
	}
	return changed, nil
}

// BaoInitialised reports whether dragonrun holds the keys to the OpenBao
// storage backend. Without them it can neither unseal nor re-initialise.
func (c *Config) BaoInitialised() bool {
	return c.Services.BaoUnsealKey != "" && c.Services.BaoRootToken != ""
}

func (s *Services) UnmarshalJSON(b []byte) error {
	type alias Services
	var a alias
	if err := json.Unmarshal(b, &a); err != nil {
		return err
	}
	*s = Services(a)
	extra, err := unknownFields(b, reflect.TypeOf(Services{}))
	if err != nil {
		return err
	}
	s.extra = extra
	return nil
}

func (s Services) MarshalJSON() ([]byte, error) {
	type alias Services
	return marshalPreserving(alias(s), s.extra)
}
