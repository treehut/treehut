// Copyright 2025 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT
package redirect

import (
	"testing"

	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLookupRepoRedirect(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	normalUser := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 5})
	ownerUser := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 20})

	testOk := func(t *testing.T, doer *user_model.User, ownerID int64, repoName string, expectedRedirectID int64) {
		t.Helper()

		redirectID, err := LookupRepoRedirect(t.Context(), doer, ownerID, repoName)
		require.NoError(t, err)
		assert.Equal(t, expectedRedirectID, redirectID)
	}

	testFail := func(t *testing.T, doer *user_model.User, ownerID int64, repoName string) {
		t.Helper()

		redirectID, err := LookupRepoRedirect(t.Context(), doer, ownerID, repoName)
		require.ErrorIs(t, err, repo_model.ErrRedirectNotExist{OwnerID: ownerID, RepoName: repoName, MissingPermission: true})
		assert.Zero(t, redirectID)
	}

	t.Run("Public repository", func(t *testing.T) {
		ownerID := int64(2)
		reponame := "oldrepo1"

		testOk(t, nil, ownerID, reponame, 1)
		testOk(t, normalUser, ownerID, reponame, 1)
		testOk(t, ownerUser, ownerID, reponame, 1)
	})

	t.Run("Private repository", func(t *testing.T) {
		ownerID := int64(17)
		reponame := "oldrepo24"

		testFail(t, nil, ownerID, reponame)
		testFail(t, normalUser, ownerID, reponame)
		testOk(t, ownerUser, ownerID, reponame, 24)
	})
}
