// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package forgery

import (
	"fmt"
	"io"
	"io/fs"
	"strings"
	"testing/fstest"
	"time"

	repo_model "forgejo.org/models/repo"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/git"
	files_service "forgejo.org/services/repository/files"
)

type MapFS = fstest.MapFS

// when a file has one of this mode, it needs specific git handling
// other non-zero modes are rejected
const (
	modeSubmodule = fs.ModeNamedPipe // hacky, but
	modeSymlink   = fs.ModeSymlink
)

func MapFile(data string) *fstest.MapFile {
	return &fstest.MapFile{
		Data: []byte(data),
	}
}

func MapSymlink(target string) *fstest.MapFile {
	return &fstest.MapFile{
		Data: []byte(target),
		Mode: modeSymlink,
	}
}

func MapSubmodule(sha string) *fstest.MapFile {
	return &fstest.MapFile{
		Data: []byte(sha),
		Mode: modeSubmodule,
	}
}

func initRepo(doer *user_model.User, repo *repo_model.Repository, format git.ObjectFormat, fsys fs.FS, commitMessage string) (string, error) {
	t, err := files_service.NewTemporaryUploadRepository(git.DefaultContext, repo)
	if err != nil {
		return "", err
	}
	defer t.Close()
	if err := t.Init(format.Name()); err != nil {
		return "", err
	}

	if err := fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}

		var content io.Reader
		mode := git.EntryModeBlob
		switch d.Type() {
		case modeSymlink:
			target, err := fs.ReadLink(fsys, path)
			if err != nil {
				return err
			}
			content = strings.NewReader(target)
			mode = git.EntryModeSymlink
		case modeSubmodule:
			mode = git.EntryModeCommit
			fallthrough
		case 0:
			f, err := fsys.Open(path)
			if err != nil {
				return err
			}
			defer f.Close()

			content = f
		default:
			return fmt.Errorf("unexpected file type in forgery.CreateRepository %s: %s", path, d.Type())
		}

		// add object to the database
		objectHash, err := t.HashObject(content)
		if err != nil {
			return err
		}
		// Add the object to the index
		return t.AddObjectToIndex(mode.String(), objectHash, path)
	}); err != nil {
		return "", err
	}

	treeHash, err := t.WriteTree()
	if err != nil {
		return "", err
	}

	now := time.Now()
	commitHash, err := t.CommitTreeWithDate("", doer, doer, treeHash, commitMessage, false, now, now)
	if err != nil {
		return "", err
	}

	return commitHash, t.Push(doer, commitHash, repo.DefaultBranch)
}
