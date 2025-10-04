// Copyright 2018 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package git

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// ParseTreeEntries parses the output of a `git ls-tree -l` command.
func ParseTreeEntries(data []byte) ([]*TreeEntry, error) {
	return parseTreeEntries(data, nil)
}

var sepSpace = []byte{' '}

func parseTreeEntries(data []byte, ptree *Tree) ([]*TreeEntry, error) {
	var err error
	entries := make([]*TreeEntry, 0, bytes.Count(data, []byte{'\n'})+1)
	for pos := 0; pos < len(data); {
		// expect line to be of the form:
		// <mode> <type> <sha> <space-padded-size>\t<filename>
		// <mode> <type> <sha>\t<filename>
		posEnd := bytes.IndexByte(data[pos:], '\n')
		if posEnd == -1 {
			posEnd = len(data)
		} else {
			posEnd += pos
		}
		line := data[pos:posEnd]
		posTab := bytes.IndexByte(line, '\t')
		if posTab == -1 {
			return nil, fmt.Errorf("invalid ls-tree output (no tab): %q", line)
		}

		entry := new(TreeEntry)
		entry.ptree = ptree

		entryAttrs := line[:posTab]
		entryName := line[posTab+1:]

		entryMode, entryAttrs, _ := bytes.Cut(entryAttrs, sepSpace)
		_ /* entryType */, entryAttrs, _ = bytes.Cut(entryAttrs, sepSpace) // the type is not used, the mode is enough to determine the type
		entryObjectID, entryAttrs, _ := bytes.Cut(entryAttrs, sepSpace)
		if len(entryAttrs) > 0 {
			entrySize := entryAttrs // the last field is the space-padded-size
			entry.size, _ = strconv.ParseInt(strings.TrimSpace(string(entrySize)), 10, 64)
			entry.sized = true
		}

		entry.entryMode, err = parseMode(string(entryMode))
		if err != nil {
			return nil, err
		}

		entry.ID, err = NewIDFromString(string(entryObjectID))
		if err != nil {
			return nil, fmt.Errorf("invalid ls-tree output (invalid object id): %q, err: %w", line, err)
		}

		if len(entryName) > 0 && entryName[0] == '"' {
			entry.name, err = strconv.Unquote(string(entryName))
			if err != nil {
				return nil, fmt.Errorf("invalid ls-tree output (invalid name): %q, err: %w", line, err)
			}
		} else {
			entry.name = string(entryName)
		}

		pos = posEnd + 1
		entries = append(entries, entry)
	}
	return entries, nil
}

func catBatchParseTreeEntries(objectFormat ObjectFormat, ptree *Tree, rd *bufio.Reader, sz int64) ([]*TreeEntry, error) {
	fnameBuf := make([]byte, 4096)
	modeBuf := make([]byte, 40)
	shaBuf := make([]byte, objectFormat.FullLength())
	entries := make([]*TreeEntry, 0, 10)

loop:
	for sz > 0 {
		mode, fname, sha, count, err := ParseTreeLine(objectFormat, rd, modeBuf, fnameBuf, shaBuf)
		if err != nil {
			if err == io.EOF {
				break loop
			}
			return nil, err
		}
		sz -= int64(count)
		entry := new(TreeEntry)
		entry.ptree = ptree
		entry.entryMode, err = parseMode(string(mode))
		if err != nil {
			return nil, err
		}
		entry.ID = objectFormat.MustID(sha)
		entry.name = string(fname)
		entries = append(entries, entry)
	}
	if _, err := rd.Discard(1); err != nil {
		return entries, err
	}

	return entries, nil
}

// Parse the file mode, we cannot hardcode the modes that we expect for
// a variety of reasons (that is not known to us) the permissions bits
// of files can vary, usually the result because of tooling that uses Git in
// a funny way. So we have to parse the mode as a integer and do bit tricks.
func parseMode(modeStr string) (EntryMode, error) {
	mode, err := strconv.ParseUint(modeStr, 8, 64)
	if err != nil {
		return 0, fmt.Errorf("cannot parse mode: %v", err)
	}

	switch mode & 0o170000 {
	case 0o040000:
		return EntryModeTree, nil
	case 0o120000:
		return EntryModeSymlink, nil
	case 0o160000:
		return EntryModeCommit, nil
	case 0o100000:
		// Check for the permission bit on the owner.
		if mode&0o100 == 0o100 {
			return EntryModeExec, nil
		}
		return EntryModeBlob, nil
	}

	return 0, fmt.Errorf("unknown mode: %o", mode)
}
