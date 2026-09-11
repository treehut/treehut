// Copyright 2026 The Treehut Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package base

import (
	"strings"

	"forgejo.org/modules/git"
	"forgejo.org/modules/log"
)

// Fallbacks for entries with no specific association.
const (
	catppuccinFileIcon          = "ctp-file"
	catppuccinFolderIcon        = "ctp-folder"
	catppuccinSubmoduleIcon     = "ctp-folder-git"
	catppuccinFolderSymlinkIcon = "ctp-folder-symlink"
	catppuccinFileSymlinkIcon   = "ctp-symlink"
)

// CatppuccinIcon returns the svg name to render for a tree entry in the
// repository file list.
//
// This is the treehut replacement for EntryIcon, which returns one of five
// generic octicons and has no per-filetype logic at all. The lookup tables are
// generated from github.com/catppuccin/vscode-icons; see
// tools/fetch-catppuccin-icons.mjs.
//
// Unlike EntryIcon the return value is a complete svg name, not a suffix to be
// concatenated after "octicon-", because the icons come from several naming
// families.
func CatppuccinIcon(entry *git.TreeEntry) string {
	switch {
	case entry.IsLink():
		te, err := entry.FollowLink()
		if err != nil {
			// Broken symlink: still better to show something than to fail the page.
			log.Debug("CatppuccinIcon: %v", err)
			return catppuccinFileSymlinkIcon
		}
		if te.IsDir() {
			return catppuccinFolderSymlinkIcon
		}
		return catppuccinFileSymlinkIcon
	case entry.IsSubmodule():
		return catppuccinSubmoduleIcon
	case entry.IsDir():
		return catppuccinFolderIconFor(entry.Name())
	}

	return catppuccinFileIconFor(entry.Name())
}

func catppuccinFolderIconFor(name string) string {
	if icon, ok := catppuccinByFolder[strings.ToLower(name)]; ok {
		return icon
	}
	return catppuccinFolderIcon
}

func catppuccinFileIconFor(name string) string {
	lower := strings.ToLower(name)

	// An exact filename match is more specific than an extension, and is the only
	// thing that can identify files like "Dockerfile" or ".gitignore".
	if icon, ok := catppuccinByFilename[lower]; ok {
		return icon
	}

	// Try progressively shorter extensions so compound suffixes win where they
	// exist: "component.test.ts" should prefer a "test.ts" association over "ts".
	// Starting after the first dot also means a leading-dot file such as
	// ".eslintrc.json" is matched on "json" rather than on the whole name.
	for i := strings.Index(lower, "."); i != -1 && i < len(lower)-1; {
		ext := lower[i+1:]
		if icon, ok := catppuccinByExtension[ext]; ok {
			return icon
		}
		next := strings.Index(ext, ".")
		if next == -1 {
			break
		}
		i += next + 1
	}

	return catppuccinFileIcon
}
