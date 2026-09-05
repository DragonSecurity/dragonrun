package registry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The incident this file exists for: `dragonrun bind 0.0.0.0` wrote the
// setting, a command run from an older binary on PATH loaded the file, did not
// know the field, and saved it back WITHOUT it. The edge silently returned to
// loopback at the next `up`, and nothing reported the loss.
//
// The keys here stand in for whatever a future version adds.
const fromNewerVersion = `{
  "domain": "test",
  "dns": "external",
  "future_setting": "keep me",
  "ports": {"postgres": 5432, "future_port": 9999},
  "superuser": "dragon",
  "superuser_password": "s3cret",
  "pgbouncer_auth_password": "b0uncer",
  "projects": {
    "scalelock": {
      "name": "scalelock",
      "role": "scalelock",
      "db": "scalelock",
      "upstream": 8080,
      "future_project_field": {"nested": true}
    }
  }
}`

func roundTrip(t *testing.T, in string) map[string]json.RawMessage {
	t.Helper()
	var c Config
	if err := json.Unmarshal([]byte(in), &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	out, err := json.Marshal(&c)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("re-unmarshal produced invalid JSON: %v\n%s", err, out)
	}
	return got
}

func TestUnknownTopLevelFieldSurvives(t *testing.T) {
	got := roundTrip(t, fromNewerVersion)
	if string(got["future_setting"]) != `"keep me"` {
		t.Errorf("future_setting = %s, want \"keep me\" — an older binary would have deleted it",
			got["future_setting"])
	}
	// And the fields this version DOES know must still be written normally.
	if string(got["domain"]) != `"test"` {
		t.Errorf("domain = %s", got["domain"])
	}
}

func TestUnknownProjectFieldSurvives(t *testing.T) {
	got := roundTrip(t, fromNewerVersion)
	var projects map[string]map[string]json.RawMessage
	if err := json.Unmarshal(got["projects"], &projects); err != nil {
		t.Fatal(err)
	}
	p := projects["scalelock"]
	if string(p["future_project_field"]) != `{"nested":true}` {
		t.Errorf("future_project_field = %s, want the nested object back verbatim",
			p["future_project_field"])
	}
	if string(p["upstream"]) != "8080" {
		t.Errorf("upstream = %s", p["upstream"])
	}
}

func TestUnknownPortSurvives(t *testing.T) {
	got := roundTrip(t, fromNewerVersion)
	var ports map[string]json.RawMessage
	if err := json.Unmarshal(got["ports"], &ports); err != nil {
		t.Fatal(err)
	}
	if string(ports["future_port"]) != "9999" {
		t.Errorf("future_port = %s, want 9999", ports["future_port"])
	}
}

// Saving an unchanged registry must produce an identical file, or every command
// that touches it leaves a spurious diff behind.
func TestRoundTripIsStable(t *testing.T) {
	var c Config
	if err := json.Unmarshal([]byte(fromNewerVersion), &c); err != nil {
		t.Fatal(err)
	}
	first, err := json.MarshalIndent(&c, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	var again Config
	if err := json.Unmarshal(first, &again); err != nil {
		t.Fatal(err)
	}
	second, err := json.MarshalIndent(&again, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Errorf("a second save differs from the first:\n%s\n---\n%s", first, second)
	}
}

// A registry with nothing unknown in it must marshal exactly as it did before
// this mechanism existed -- no empty objects, no stray commas.
func TestNothingUnknownIsUnchanged(t *testing.T) {
	c := Config{Domain: "test", DNS: DNSExternal, Bind: DefaultBind,
		Ports: DefaultPorts(), Projects: map[string]Project{}}
	b, err := json.Marshal(&c)
	if err != nil {
		t.Fatal(err)
	}
	var back Config
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("%v: %s", err, b)
	}
	if back.Domain != "test" || back.Bind != DefaultBind {
		t.Errorf("round trip changed a known field: %+v", back)
	}
}

// The whole point is the file on disk, so exercise Load and Save rather than
// only the marshallers.
func TestLoadSavePreservesUnknownFields(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DRAGONRUN_HOME", home)
	if err := os.WriteFile(filepath.Join(home, "registry.json"),
		[]byte(fromNewerVersion), 0o600); err != nil {
		t.Fatal(err)
	}

	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(filepath.Join(home, "registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if _, ok := got["future_setting"]; !ok {
		t.Errorf("Save() dropped future_setting — this is the bug:\n%s", b)
	}
}
