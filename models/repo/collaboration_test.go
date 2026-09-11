// Copyright 2022 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package repo_test

import (
	"testing"

	"forgejo.org/models/db"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRepository_GetCollaborators(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	test := func(repoID int64) {
		repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: repoID})
		collaborators, err := repo_model.GetCollaborators(db.DefaultContext, repo.ID, db.ListOptions{})
		require.NoError(t, err)
		expectedLen, err := db.GetEngine(db.DefaultContext).Count(&repo_model.Collaboration{RepoID: repoID})
		require.NoError(t, err)
		assert.Len(t, collaborators, int(expectedLen))
		for _, collaborator := range collaborators {
			assert.Equal(t, collaborator.User.ID, collaborator.Collaboration.UserID)
			assert.Equal(t, repoID, collaborator.Collaboration.RepoID)
		}
	}
	test(1)
	test(2)
	test(3)
	test(4)

	// Test db.ListOptions
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 22})

	collaborators1, err := repo_model.GetCollaborators(db.DefaultContext, repo.ID, db.ListOptions{PageSize: 1, Page: 1})
	require.NoError(t, err)
	assert.Len(t, collaborators1, 1)

	collaborators2, err := repo_model.GetCollaborators(db.DefaultContext, repo.ID, db.ListOptions{PageSize: 1, Page: 2})
	require.NoError(t, err)
	assert.Len(t, collaborators2, 1)

	assert.NotEqual(t, collaborators1[0].ID, collaborators2[0].ID)
}

func TestRepository_IsCollaborator(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	test := func(repoID, userID int64, expected bool) {
		repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: repoID})
		actual, err := repo_model.IsCollaborator(db.DefaultContext, repo.ID, userID)
		require.NoError(t, err)
		assert.Equal(t, expected, actual)
	}
	test(3, 2, true)
	test(3, unittest.NonexistentID, false)
	test(4, 2, false)
	test(4, 4, true)
}

func TestRepository_CountCollaborators(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	repo1 := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 4})
	count, err := db.Count[repo_model.Collaboration](db.DefaultContext, repo_model.FindCollaborationOptions{
		RepoID: repo1.ID,
	})
	require.NoError(t, err)
	assert.EqualValues(t, 2, count)

	repo2 := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 22})
	count, err = db.Count[repo_model.Collaboration](db.DefaultContext, repo_model.FindCollaborationOptions{
		RepoID: repo2.ID,
	})
	require.NoError(t, err)
	assert.EqualValues(t, 2, count)

	// Non-existent repository.
	count, err = db.Count[repo_model.Collaboration](db.DefaultContext, repo_model.FindCollaborationOptions{
		RepoID: unittest.NonexistentID,
	})
	require.NoError(t, err)
	assert.EqualValues(t, 0, count)
}

func TestRepository_IsOwnerMemberCollaborator(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	repo1 := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 3})

	// Organisation owner.
	actual, err := repo_model.IsOwnerMemberCollaborator(db.DefaultContext, repo1, 2)
	require.NoError(t, err)
	assert.True(t, actual)

	// Team member.
	actual, err = repo_model.IsOwnerMemberCollaborator(db.DefaultContext, repo1, 4)
	require.NoError(t, err)
	assert.True(t, actual)

	// Normal user.
	actual, err = repo_model.IsOwnerMemberCollaborator(db.DefaultContext, repo1, 1)
	require.NoError(t, err)
	assert.False(t, actual)

	repo2 := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 4})

	// Collaborator.
	actual, err = repo_model.IsOwnerMemberCollaborator(db.DefaultContext, repo2, 4)
	require.NoError(t, err)
	assert.True(t, actual)

	repo3 := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 15})

	// Repository owner.
	actual, err = repo_model.IsOwnerMemberCollaborator(db.DefaultContext, repo3, 2)
	require.NoError(t, err)
	assert.True(t, actual)
}

func TestGetCollaboratorWithUser(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	user16 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 16})
	user15 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 15})
	user18 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 18})

	collabs, err := repo_model.GetCollaboratorWithUser(db.DefaultContext, user16.ID, user15.ID)
	require.NoError(t, err)
	assert.Len(t, collabs, 2)
	assert.EqualValues(t, 5, collabs[0])
	assert.EqualValues(t, 7, collabs[1])

	collabs, err = repo_model.GetCollaboratorWithUser(db.DefaultContext, user16.ID, user18.ID)
	require.NoError(t, err)
	assert.Len(t, collabs, 2)
	assert.EqualValues(t, 6, collabs[0])
	assert.EqualValues(t, 8, collabs[1])
}
