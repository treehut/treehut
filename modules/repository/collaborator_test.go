// Copyright 2019 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package repository

import (
	"testing"

	"forgejo.org/models/db"
	"forgejo.org/models/organization"
	perm_model "forgejo.org/models/perm"
	access_model "forgejo.org/models/perm/access"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unit"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRepository_AddCollaborator(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	testSuccess := func(repoID, userID int64) {
		repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: repoID})
		require.NoError(t, repo.LoadOwner(db.DefaultContext))
		user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: userID})
		require.NoError(t, AddCollaborator(db.DefaultContext, repo, user))
		unittest.CheckConsistencyFor(t, &repo_model.Repository{ID: repoID}, &user_model.User{ID: userID})
	}
	testSuccess(1, 4)
	testSuccess(1, 4)
	testSuccess(3, 4)
}

func TestRepository_AddCollaborator_IsBlocked(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	testSuccess := func(repoID, userID int64) {
		repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: repoID})
		require.NoError(t, repo.LoadOwner(db.DefaultContext))
		user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: userID})

		// Owner blocked user.
		unittest.AssertSuccessfulInsert(t, &user_model.BlockedUser{UserID: repo.OwnerID, BlockID: userID})
		require.ErrorIs(t, AddCollaborator(db.DefaultContext, repo, user), user_model.ErrBlockedByUser)
		unittest.CheckConsistencyFor(t, &repo_model.Repository{ID: repoID}, &user_model.User{ID: userID})
		_, err := db.DeleteByBean(db.DefaultContext, &user_model.BlockedUser{UserID: repo.OwnerID, BlockID: userID})
		require.NoError(t, err)

		// User has owner blocked.
		unittest.AssertSuccessfulInsert(t, &user_model.BlockedUser{UserID: userID, BlockID: repo.OwnerID})
		require.ErrorIs(t, AddCollaborator(db.DefaultContext, repo, user), user_model.ErrBlockedByUser)
		unittest.CheckConsistencyFor(t, &repo_model.Repository{ID: repoID}, &user_model.User{ID: userID})
	}
	// Ensure idempotency (public repository).
	testSuccess(1, 4)
	testSuccess(1, 4)
	// Add collaborator to private repository.
	testSuccess(3, 4)
}

func TestRepoPermissionPublicNonOrgRepo(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	// public non-organization repo
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 4})
	require.NoError(t, repo.LoadUnits(db.DefaultContext))

	// plain user
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	perm, err := access_model.GetUserRepoPermission(db.DefaultContext, repo, user)
	require.NoError(t, err)
	for _, unit := range repo.Units {
		assert.True(t, perm.CanRead(unit.Type))
		assert.False(t, perm.CanWrite(unit.Type))
	}

	// change to collaborator
	require.NoError(t, AddCollaborator(db.DefaultContext, repo, user))
	perm, err = access_model.GetUserRepoPermission(db.DefaultContext, repo, user)
	require.NoError(t, err)
	for _, unit := range repo.Units {
		assert.True(t, perm.CanRead(unit.Type))
		assert.True(t, perm.CanWrite(unit.Type))
	}

	// collaborator
	collaborator := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 4})
	perm, err = access_model.GetUserRepoPermission(db.DefaultContext, repo, collaborator)
	require.NoError(t, err)
	for _, unit := range repo.Units {
		assert.True(t, perm.CanRead(unit.Type))
		assert.True(t, perm.CanWrite(unit.Type))
	}

	// owner
	owner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 5})
	perm, err = access_model.GetUserRepoPermission(db.DefaultContext, repo, owner)
	require.NoError(t, err)
	for _, unit := range repo.Units {
		assert.True(t, perm.CanRead(unit.Type))
		assert.True(t, perm.CanWrite(unit.Type))
	}

	// admin
	admin := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
	perm, err = access_model.GetUserRepoPermission(db.DefaultContext, repo, admin)
	require.NoError(t, err)
	for _, unit := range repo.Units {
		assert.True(t, perm.CanRead(unit.Type))
		assert.True(t, perm.CanWrite(unit.Type))
	}
}

func TestRepoPermissionPrivateNonOrgRepo(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	// private non-organization repo
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 2})
	require.NoError(t, repo.LoadUnits(db.DefaultContext))

	// plain user
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 4})
	perm, err := access_model.GetUserRepoPermission(db.DefaultContext, repo, user)
	require.NoError(t, err)
	for _, unit := range repo.Units {
		assert.False(t, perm.CanRead(unit.Type))
		assert.False(t, perm.CanWrite(unit.Type))
	}

	// change to collaborator to default write access
	require.NoError(t, AddCollaborator(db.DefaultContext, repo, user))
	perm, err = access_model.GetUserRepoPermission(db.DefaultContext, repo, user)
	require.NoError(t, err)
	for _, unit := range repo.Units {
		assert.True(t, perm.CanRead(unit.Type))
		assert.True(t, perm.CanWrite(unit.Type))
	}

	require.NoError(t, ChangeCollaborationAccessMode(db.DefaultContext, repo, user.ID, perm_model.AccessModeRead))
	perm, err = access_model.GetUserRepoPermission(db.DefaultContext, repo, user)
	require.NoError(t, err)
	for _, unit := range repo.Units {
		assert.True(t, perm.CanRead(unit.Type))
		assert.False(t, perm.CanWrite(unit.Type))
	}

	// owner
	owner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	perm, err = access_model.GetUserRepoPermission(db.DefaultContext, repo, owner)
	require.NoError(t, err)
	for _, unit := range repo.Units {
		assert.True(t, perm.CanRead(unit.Type))
		assert.True(t, perm.CanWrite(unit.Type))
	}

	// admin
	admin := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
	perm, err = access_model.GetUserRepoPermission(db.DefaultContext, repo, admin)
	require.NoError(t, err)
	for _, unit := range repo.Units {
		assert.True(t, perm.CanRead(unit.Type))
		assert.True(t, perm.CanWrite(unit.Type))
	}
}

func TestRepoPermissionPublicOrgRepo(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	// public organization repo
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 32})
	require.NoError(t, repo.LoadUnits(db.DefaultContext))

	// plain user
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 5})
	perm, err := access_model.GetUserRepoPermission(db.DefaultContext, repo, user)
	require.NoError(t, err)
	for _, unit := range repo.Units {
		assert.True(t, perm.CanRead(unit.Type))
		assert.False(t, perm.CanWrite(unit.Type))
	}

	// change to collaborator to default write access
	require.NoError(t, AddCollaborator(db.DefaultContext, repo, user))
	perm, err = access_model.GetUserRepoPermission(db.DefaultContext, repo, user)
	require.NoError(t, err)
	for _, unit := range repo.Units {
		assert.True(t, perm.CanRead(unit.Type))
		assert.True(t, perm.CanWrite(unit.Type))
	}

	require.NoError(t, ChangeCollaborationAccessMode(db.DefaultContext, repo, user.ID, perm_model.AccessModeRead))
	perm, err = access_model.GetUserRepoPermission(db.DefaultContext, repo, user)
	require.NoError(t, err)
	for _, unit := range repo.Units {
		assert.True(t, perm.CanRead(unit.Type))
		assert.False(t, perm.CanWrite(unit.Type))
	}

	// org member team owner
	owner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	perm, err = access_model.GetUserRepoPermission(db.DefaultContext, repo, owner)
	require.NoError(t, err)
	for _, unit := range repo.Units {
		assert.True(t, perm.CanRead(unit.Type))
		assert.True(t, perm.CanWrite(unit.Type))
	}

	// org member team tester
	member := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 15})
	perm, err = access_model.GetUserRepoPermission(db.DefaultContext, repo, member)
	require.NoError(t, err)
	for _, unit := range repo.Units {
		assert.True(t, perm.CanRead(unit.Type))
	}
	assert.True(t, perm.CanWrite(unit.TypeIssues))
	assert.False(t, perm.CanWrite(unit.TypeCode))

	// admin
	admin := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
	perm, err = access_model.GetUserRepoPermission(db.DefaultContext, repo, admin)
	require.NoError(t, err)
	for _, unit := range repo.Units {
		assert.True(t, perm.CanRead(unit.Type))
		assert.True(t, perm.CanWrite(unit.Type))
	}
}

func TestRepoPermissionPrivateOrgRepo(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	// private organization repo
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 24})
	require.NoError(t, repo.LoadUnits(db.DefaultContext))

	// plain user
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 5})
	perm, err := access_model.GetUserRepoPermission(db.DefaultContext, repo, user)
	require.NoError(t, err)
	for _, unit := range repo.Units {
		assert.False(t, perm.CanRead(unit.Type))
		assert.False(t, perm.CanWrite(unit.Type))
	}

	// change to collaborator to default write access
	require.NoError(t, AddCollaborator(db.DefaultContext, repo, user))
	perm, err = access_model.GetUserRepoPermission(db.DefaultContext, repo, user)
	require.NoError(t, err)
	for _, unit := range repo.Units {
		assert.True(t, perm.CanRead(unit.Type))
		assert.True(t, perm.CanWrite(unit.Type))
	}

	require.NoError(t, ChangeCollaborationAccessMode(db.DefaultContext, repo, user.ID, perm_model.AccessModeRead))
	perm, err = access_model.GetUserRepoPermission(db.DefaultContext, repo, user)
	require.NoError(t, err)
	for _, unit := range repo.Units {
		assert.True(t, perm.CanRead(unit.Type))
		assert.False(t, perm.CanWrite(unit.Type))
	}

	// org member team owner
	owner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 15})
	perm, err = access_model.GetUserRepoPermission(db.DefaultContext, repo, owner)
	require.NoError(t, err)
	for _, unit := range repo.Units {
		assert.True(t, perm.CanRead(unit.Type))
		assert.True(t, perm.CanWrite(unit.Type))
	}

	// update team information and then check permission
	team := unittest.AssertExistsAndLoadBean(t, &organization.Team{ID: 5})
	unittest.AssertSuccessfulDelete(t, &organization.TeamUnit{TeamID: team.ID})
	perm, err = access_model.GetUserRepoPermission(db.DefaultContext, repo, owner)
	require.NoError(t, err)
	for _, unit := range repo.Units {
		assert.True(t, perm.CanRead(unit.Type))
		assert.True(t, perm.CanWrite(unit.Type))
	}

	// org member team tester
	tester := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	perm, err = access_model.GetUserRepoPermission(db.DefaultContext, repo, tester)
	require.NoError(t, err)
	assert.True(t, perm.CanWrite(unit.TypeIssues))
	assert.False(t, perm.CanWrite(unit.TypeCode))
	assert.False(t, perm.CanRead(unit.TypeCode))

	// org member team reviewer
	reviewer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 20})
	perm, err = access_model.GetUserRepoPermission(db.DefaultContext, repo, reviewer)
	require.NoError(t, err)
	assert.False(t, perm.CanRead(unit.TypeIssues))
	assert.False(t, perm.CanWrite(unit.TypeCode))
	assert.True(t, perm.CanRead(unit.TypeCode))

	// admin
	admin := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
	perm, err = access_model.GetUserRepoPermission(db.DefaultContext, repo, admin)
	require.NoError(t, err)
	for _, unit := range repo.Units {
		assert.True(t, perm.CanRead(unit.Type))
		assert.True(t, perm.CanWrite(unit.Type))
	}
}

func TestRepository_ChangeCollaborationAccessMode(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 4})
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 4})

	// Set to Admin
	require.NoError(t, ChangeCollaborationAccessMode(db.DefaultContext, repo, user.ID, perm_model.AccessModeAdmin))
	collaboration := unittest.AssertExistsAndLoadBean(t, &repo_model.Collaboration{RepoID: repo.ID, UserID: user.ID})
	assert.Equal(t, perm_model.AccessModeAdmin, collaboration.Mode)
	access := unittest.AssertExistsAndLoadBean(t, &access_model.Access{UserID: user.ID, RepoID: repo.ID})
	assert.Equal(t, perm_model.AccessModeAdmin, access.Mode)

	// Repeat setting to the same value, ensure no errors
	require.NoError(t, ChangeCollaborationAccessMode(db.DefaultContext, repo, user.ID, perm_model.AccessModeAdmin))

	// Reduce collaborator to Read access, validate collaboration & access is updated
	require.NoError(t, ChangeCollaborationAccessMode(db.DefaultContext, repo, user.ID, perm_model.AccessModeWrite))
	collaboration = unittest.AssertExistsAndLoadBean(t, &repo_model.Collaboration{RepoID: repo.ID, UserID: user.ID})
	assert.Equal(t, perm_model.AccessModeWrite, collaboration.Mode)
	access = unittest.AssertExistsAndLoadBean(t, &access_model.Access{UserID: user.ID, RepoID: repo.ID})
	assert.Equal(t, perm_model.AccessModeWrite, access.Mode)

	// Ensure no error on invalid user ID.
	require.NoError(t, ChangeCollaborationAccessMode(db.DefaultContext, repo, unittest.NonexistentID, perm_model.AccessModeAdmin))

	// Ensure discarded on invalid access mode.
	require.NoError(t, ChangeCollaborationAccessMode(db.DefaultContext, repo, user.ID, perm_model.AccessMode(unittest.NonexistentID)))

	// On an organization-owned repo, access can be granted through a team, or a collaborator.  The highest available
	// access mode should win and be stored in the access table.  First set-up collaborator with Admin:
	repo = unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 3})
	require.NoError(t, AddCollaborator(t.Context(), repo, user))
	require.NoError(t, ChangeCollaborationAccessMode(t.Context(), repo, user.ID, perm_model.AccessModeAdmin))
	collaboration = unittest.AssertExistsAndLoadBean(t, &repo_model.Collaboration{RepoID: repo.ID, UserID: user.ID})
	assert.Equal(t, perm_model.AccessModeAdmin, collaboration.Mode)
	access = unittest.AssertExistsAndLoadBean(t, &access_model.Access{UserID: user.ID, RepoID: repo.ID})
	assert.Equal(t, perm_model.AccessModeAdmin, access.Mode)

	// Drop the collaborator access to read.  While the collab record should drop to read, the access record should
	// remain at write, a permission granted by team membership:
	require.NoError(t, ChangeCollaborationAccessMode(t.Context(), repo, user.ID, perm_model.AccessModeRead))
	collaboration = unittest.AssertExistsAndLoadBean(t, &repo_model.Collaboration{RepoID: repo.ID, UserID: user.ID})
	assert.Equal(t, perm_model.AccessModeRead, collaboration.Mode)
	access = unittest.AssertExistsAndLoadBean(t, &access_model.Access{UserID: user.ID, RepoID: repo.ID})
	assert.Equal(t, perm_model.AccessModeWrite, access.Mode)

	unittest.CheckConsistencyFor(t, &repo_model.Repository{ID: repo.ID})
}

func TestRepository_DeleteCollaboration(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 4})
	require.NoError(t, repo.LoadOwner(db.DefaultContext))
	require.NoError(t, DeleteCollaboration(db.DefaultContext, repo, 4))
	unittest.AssertNotExistsBean(t, &repo_model.Collaboration{RepoID: repo.ID, UserID: 4})

	require.NoError(t, DeleteCollaboration(db.DefaultContext, repo, 4))
	unittest.AssertNotExistsBean(t, &repo_model.Collaboration{RepoID: repo.ID, UserID: 4})

	unittest.CheckConsistencyFor(t, &repo_model.Repository{ID: repo.ID})
}
