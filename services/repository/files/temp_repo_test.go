// Copyright 2024 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package files

import (
	"testing"

	"forgejo.org/models/db"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unittest"
	"forgejo.org/modules/git"

	"github.com/stretchr/testify/require"
)

func TestTemporaryUploadRepositoryRemoveFilesFromIndexSha256(t *testing.T) {
	if git.CheckGitVersionAtLeast("2.42") != nil {
		t.Skip("skipping because installed Git version doesn't support SHA256")
	}
	unittest.PrepareTestEnv(t)
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})

	temp, err := NewTemporaryUploadRepository(db.DefaultContext, repo)
	require.NoError(t, err)
	require.NoError(t, temp.Init("sha256"))
	require.NoError(t, temp.RemoveFilesFromIndex("README.md"))
}

func TestTemporaryUploadRepositoryAddObjectToIndex(t *testing.T) {
	unittest.PrepareTestEnv(t)
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})

	temp, err := NewTemporaryUploadRepository(db.DefaultContext, repo)
	require.NoError(t, err)
	require.NoError(t, temp.Init("sha1"))
	require.NoError(t, temp.AddObjectToIndex("100644", "e69de29bb2d1d6434b8b29ae775ad8c2e48c5391", "--test"))
}
