package registry

import (
	"bytes"
	"encoding/json"
	"reflect"
	"sort"
	"strings"
)

// Forward compatibility for registry.json.
//
// The registry outlives any one binary. Two dragonruns on the same machine, a
// DRAGONRUN_HOME on a synced directory, or simply an upgrade followed by one
// command run from an older build on PATH -- in every case a binary reads a
// file written by a different version.
//
// Unmarshalling into a struct silently discards keys the struct has no field
// for, and marshalling it back writes the file WITHOUT them. So an older
// dragonrun does not merely ignore a newer setting, it deletes it: the edge
// bind address disappears and the stack quietly returns to loopback at the
// next `up`; no_site disappears and a database-only project starts claiming a
// host port again. Nothing reports this, because from the old binary's point
// of view the file simply never had those keys.
//
// The fix is for every struct in the file to carry the keys it did not
// recognise and write them back out. A field added in a later version then
// survives a round trip through an earlier one.

// preserve holds the JSON this version has no field for, verbatim.
type preserve map[string]json.RawMessage

// unknownFields returns the members of a JSON object that no field of t claims.
func unknownFields(b []byte, t reflect.Type) (preserve, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, err
	}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" {
			continue // unexported: never in the JSON to begin with
		}
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "" {
			name = f.Name // no tag means encoding/json uses the field name
		}
		if name == "-" {
			continue
		}
		delete(raw, name)
	}
	if len(raw) == 0 {
		return nil, nil
	}
	return raw, nil
}

// marshalPreserving marshals v and appends the preserved members.
//
// Splicing onto the encoder's own output rather than round-tripping through a
// map keeps the known fields in struct order, which is what makes the file
// readable; the unknown ones follow in sorted order so a save is stable and
// re-saving an unchanged registry produces an identical file.
func marshalPreserving(v any, extra preserve) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	if len(extra) == 0 {
		return b, nil
	}
	keys := make([]string, 0, len(extra))
	for k := range extra {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var out bytes.Buffer
	out.Write(b[:len(b)-1]) // everything but the closing brace
	for _, k := range keys {
		if out.Len() > 1 { // ">1" is "the object is not empty": just "{" so far
			out.WriteByte(',')
		}
		key, err := json.Marshal(k)
		if err != nil {
			return nil, err
		}
		out.Write(key)
		out.WriteByte(':')
		out.Write(extra[k])
	}
	out.WriteByte('}')
	return out.Bytes(), nil
}

// The three structs that make up registry.json each carry their own unknown
// keys: a field could be added at any level, and preserving only the top one
// would still lose a new per-project or per-port setting.
//
// The `type alias X` in each pair is what stops the custom methods recursing
// into themselves -- a defined type does not inherit its source type's method
// set, so encoding/json falls back to struct reflection for it.

func (c *Config) UnmarshalJSON(b []byte) error {
	type alias Config
	var a alias
	if err := json.Unmarshal(b, &a); err != nil {
		return err
	}
	*c = Config(a)
	extra, err := unknownFields(b, reflect.TypeOf(Config{}))
	if err != nil {
		return err
	}
	c.extra = extra
	return nil
}

func (c Config) MarshalJSON() ([]byte, error) {
	type alias Config
	return marshalPreserving(alias(c), c.extra)
}

func (p *Project) UnmarshalJSON(b []byte) error {
	type alias Project
	var a alias
	if err := json.Unmarshal(b, &a); err != nil {
		return err
	}
	*p = Project(a)
	extra, err := unknownFields(b, reflect.TypeOf(Project{}))
	if err != nil {
		return err
	}
	p.extra = extra
	return nil
}

func (p Project) MarshalJSON() ([]byte, error) {
	type alias Project
	return marshalPreserving(alias(p), p.extra)
}

func (p *Ports) UnmarshalJSON(b []byte) error {
	type alias Ports
	var a alias
	if err := json.Unmarshal(b, &a); err != nil {
		return err
	}
	*p = Ports(a)
	extra, err := unknownFields(b, reflect.TypeOf(Ports{}))
	if err != nil {
		return err
	}
	p.extra = extra
	return nil
}

func (p Ports) MarshalJSON() ([]byte, error) {
	type alias Ports
	return marshalPreserving(alias(p), p.extra)
}
