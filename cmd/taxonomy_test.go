package cmd

import (
	"strings"
	"testing"
)

// install/uninstall must never resolve to anything that acts. `uninstall` in
// particular used to destroy every database, while reading like "remove the
// tool" -- the kind of wrong guess that has to fail loudly.
func TestLegacyNamesRefuseToAct(t *testing.T) {
	for _, name := range []string{"install", "uninstall"} {
		c, _, err := root.Find([]string{name})
		if err != nil || c.Name() != name {
			t.Fatalf("%q did not resolve to its guard command", name)
		}
		if !c.Hidden {
			t.Errorf("%q should be hidden from help", name)
		}
		if c.RunE == nil {
			t.Fatalf("%q has no RunE — it would silently do nothing", name)
		}
		err = c.RunE(c, nil)
		if err == nil {
			t.Fatalf("%q returned no error — it must refuse", name)
		}
		if !strings.Contains(err.Error(), "not a dragonrun command") {
			t.Errorf("%q error should say so plainly, got: %v", name, err)
		}
	}
	// And the real destructive command must be reachable only by its own name.
	if c, _, err := root.Find([]string{"destroy"}); err != nil || c.Name() != "destroy" {
		t.Error("destroy is not reachable")
	}
}

// Every user-facing command belongs to exactly one scope, so help states what
// each one touches.
func TestEveryCommandHasAGroup(t *testing.T) {
	valid := map[string]bool{groupMachine: true, groupStack: true, groupProject: true}
	builtin := map[string]bool{"help": true, "completion": true}
	for _, c := range root.Commands() {
		if c.Hidden || builtin[c.Name()] {
			continue
		}
		if !valid[c.GroupID] {
			t.Errorf("command %q has GroupID %q — assign it a scope", c.Name(), c.GroupID)
		}
	}
}

// new/delete is the documented pair; the older names must keep working.
func TestDeleteKeepsItsAliases(t *testing.T) {
	for _, name := range []string{"delete", "remove", "rm"} {
		c, _, err := root.Find([]string{name})
		if err != nil || c.Name() != "delete" {
			t.Errorf("%q did not resolve to delete", name)
		}
	}
}
