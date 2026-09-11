// Copyright 2026 The Treehut Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package base

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCatppuccinFileIconFor(t *testing.T) {
	cases := []struct {
		name     string
		expected string
		why      string
	}{
		{"main.go", "ctp-go", "plain extension"},
		{"index.ts", "ctp-typescript", "plain extension"},
		{"README.md", "ctp-readme", "filename association wins over the .md extension"},
		{"Notes.MD", "ctp-markdown", "extension match is case-insensitive"},
		{"Dockerfile", "ctp-docker", "exact filename, no extension at all"},
		{".gitignore", "ctp-git", "leading-dot filename"},
		{"package.json", "ctp-package-json", "exact filename beats the .json extension"},
		{"data.json", "ctp-json", "extension when no filename association exists"},
		{"nonsense.zzzzz", "ctp-file", "unknown extension falls back"},
		{"noextension", "ctp-file", "no extension at all falls back"},
		{"", "ctp-file", "empty name must not panic"},
		{"trailing.", "ctp-file", "trailing dot must not panic or match"},
	}
	for _, c := range cases {
		assert.Equal(t, c.expected, catppuccinFileIconFor(c.name), "%s (%s)", c.name, c.why)
	}
}

// Compound suffixes are the reason the extension search walks dot by dot rather
// than just taking the text after the last dot. Splitting on the final dot would
// give "ts" and "yaml" here and lose the more specific icon.
func TestCatppuccinFileIconForCompoundExtensions(t *testing.T) {
	// "d.ts" is its own association, and must win over plain "ts".
	assert.Equal(t, "ctp-typescript-def", catppuccinFileIconFor("component.d.ts"))
	assert.Equal(t, "ctp-typescript", catppuccinFileIconFor("component.ts"),
		"a single extension must still reach the plain icon")

	// A dotted *filename* association is checked before any extension.
	assert.Equal(t, "ctp-docker-compose", catppuccinFileIconFor("docker-compose.override.yaml"))
	assert.Equal(t, "ctp-yaml", catppuccinFileIconFor("something.override.yaml"),
		"an unknown compound suffix must fall through to the last known extension")
}

func TestCatppuccinFolderIconFor(t *testing.T) {
	assert.Equal(t, "ctp-folder-src", catppuccinFolderIconFor("src"))
	assert.Equal(t, "ctp-folder-src", catppuccinFolderIconFor("SRC"), "case-insensitive")
	assert.Equal(t, "ctp-folder", catppuccinFolderIconFor("no-such-folder-name"))
	assert.Equal(t, "ctp-folder", catppuccinFolderIconFor(""))
}

// Every icon the Go code names as a fallback has to actually exist, otherwise the
// file list renders a blank space. The generated tables are machine-produced, but
// these five constants are hand-written and can drift.
func TestCatppuccinFallbackIconsAreReferenced(t *testing.T) {
	for _, icon := range []string{
		catppuccinFileIcon,
		catppuccinFolderIcon,
		catppuccinSubmoduleIcon,
		catppuccinFolderSymlinkIcon,
		catppuccinFileSymlinkIcon,
	} {
		assert.True(t, len(icon) > 4 && icon[:4] == "ctp-", "%q should be a ctp- svg name", icon)
	}
}
