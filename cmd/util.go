package cmd

import (
	"path/filepath"
	"strings"
)

// filepath_Base is filepath.Base normalised the way project names are: a
// directory called "Eyrie" or "my_app" should still find project "eyrie" /
// "my-app" rather than reporting it unregistered.
func filepath_Base(p string) string {
	return strings.ToLower(strings.ReplaceAll(filepath.Base(p), "_", "-"))
}
