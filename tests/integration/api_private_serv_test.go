// Copyright 2021 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	asymkey_model "forgejo.org/models/asymkey"
	"forgejo.org/models/auth"
	"forgejo.org/models/perm"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/private"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/test"
	"forgejo.org/tests/forgery"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAPIPrivateNoServ(t *testing.T) {
	onApplicationRun(t, func(*testing.T, *url.URL) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		key, user, err := private.ServNoCommand(ctx, 1)
		require.NoError(t, err)
		assert.Equal(t, int64(2), user.ID)
		assert.Equal(t, "user2", user.Name)
		assert.Equal(t, int64(1), key.ID)
		assert.Equal(t, "user2@localhost", key.Name)

		deployKey, err := asymkey_model.AddDeployKey(ctx, 1, "test-deploy", "sk-ecdsa-sha2-nistp256@openssh.com AAAAInNrLWVjZHNhLXNoYTItbmlzdHAyNTZAb3BlbnNzaC5jb20AAAAIbmlzdHAyNTYAAABBBGXEEzWmm1dxb+57RoK5KVCL0w2eNv9cqJX2AGGVlkFsVDhOXHzsadS3LTK4VlEbbrDMJdoti9yM8vclA8IeRacAAAAEc3NoOg== nocomment", false)
		require.NoError(t, err)

		key, user, err = private.ServNoCommand(ctx, deployKey.KeyID)
		require.NoError(t, err)
		assert.Empty(t, user)
		assert.Equal(t, deployKey.KeyID, key.ID)
		assert.Equal(t, "test-deploy", key.Name)
	})
}

func TestAPIPrivateServ(t *testing.T) {
	onApplicationRun(t, func(*testing.T, *url.URL) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		// Can push to a repo we own
		results, extra := private.ServCommand(ctx, 1, "user2", "repo1", perm.AccessModeWrite, "git-upload-pack", "")
		require.NoError(t, extra.Error)
		assert.False(t, results.IsWiki)
		assert.Zero(t, results.DeployKeyID)
		assert.Equal(t, int64(1), results.KeyID)
		assert.Equal(t, "user2@localhost", results.KeyName)
		assert.Equal(t, "user2", results.UserName)
		assert.Equal(t, int64(2), results.UserID)
		assert.Equal(t, "user2", results.OwnerName)
		assert.Equal(t, "repo1", results.RepoName)
		assert.Equal(t, int64(1), results.RepoID)

		// Cannot push to a private repo we're not associated with
		results, extra = private.ServCommand(ctx, 1, "user15", "big_test_private_1", perm.AccessModeWrite, "git-upload-pack", "")
		require.Error(t, extra.Error)
		assert.Empty(t, results)

		// Cannot pull from a private repo we're not associated with
		results, extra = private.ServCommand(ctx, 1, "user15", "big_test_private_1", perm.AccessModeRead, "git-upload-pack", "")
		require.Error(t, extra.Error)
		assert.Empty(t, results)

		// Can pull from a public repo we're not associated with
		results, extra = private.ServCommand(ctx, 1, "user15", "big_test_public_1", perm.AccessModeRead, "git-upload-pack", "")
		require.NoError(t, extra.Error)
		assert.False(t, results.IsWiki)
		assert.Zero(t, results.DeployKeyID)
		assert.Equal(t, int64(1), results.KeyID)
		assert.Equal(t, "user2@localhost", results.KeyName)
		assert.Equal(t, "user2", results.UserName)
		assert.Equal(t, int64(2), results.UserID)
		assert.Equal(t, "user15", results.OwnerName)
		assert.Equal(t, "big_test_public_1", results.RepoName)
		assert.Equal(t, int64(17), results.RepoID)

		// Cannot push to a public repo we're not associated with
		results, extra = private.ServCommand(ctx, 1, "user15", "big_test_public_1", perm.AccessModeWrite, "git-upload-pack", "")
		require.Error(t, extra.Error)
		assert.Empty(t, results)

		// Add reading deploy key
		deployKey, err := asymkey_model.AddDeployKey(ctx, 19, "test-deploy", "sk-ecdsa-sha2-nistp256@openssh.com AAAAInNrLWVjZHNhLXNoYTItbmlzdHAyNTZAb3BlbnNzaC5jb20AAAAIbmlzdHAyNTYAAABBBGXEEzWmm1dxb+57RoK5KVCL0w2eNv9cqJX2AGGVlkFsVDhOXHzsadS3LTK4VlEbbrDMJdoti9yM8vclA8IeRacAAAAEc3NoOg== nocomment", true)
		require.NoError(t, err)

		// Can pull from repo we're a deploy key for
		results, extra = private.ServCommand(ctx, deployKey.KeyID, "user15", "big_test_private_1", perm.AccessModeRead, "git-upload-pack", "")
		require.NoError(t, extra.Error)
		assert.False(t, results.IsWiki)
		assert.NotZero(t, results.DeployKeyID)
		assert.Equal(t, deployKey.KeyID, results.KeyID)
		assert.Equal(t, "test-deploy", results.KeyName)
		assert.Equal(t, "user15", results.UserName)
		assert.Equal(t, int64(15), results.UserID)
		assert.Equal(t, "user15", results.OwnerName)
		assert.Equal(t, "big_test_private_1", results.RepoName)
		assert.Equal(t, int64(19), results.RepoID)

		// Cannot push to a private repo with reading key
		results, extra = private.ServCommand(ctx, deployKey.KeyID, "user15", "big_test_private_1", perm.AccessModeWrite, "git-upload-pack", "")
		require.Error(t, extra.Error)
		assert.Empty(t, results)

		// Cannot pull from a private repo we're not associated with
		results, extra = private.ServCommand(ctx, deployKey.KeyID, "user15", "big_test_private_2", perm.AccessModeRead, "git-upload-pack", "")
		require.Error(t, extra.Error)
		assert.Empty(t, results)

		// Cannot pull from a public repo we're not associated with
		results, extra = private.ServCommand(ctx, deployKey.KeyID, "user15", "big_test_public_1", perm.AccessModeRead, "git-upload-pack", "")
		require.Error(t, extra.Error)
		assert.Empty(t, results)

		// Add writing deploy key
		deployKey, err = asymkey_model.AddDeployKey(ctx, 20, "test-deploy", "sk-ecdsa-sha2-nistp256@openssh.com AAAAInNrLWVjZHNhLXNoYTItbmlzdHAyNTZAb3BlbnNzaC5jb20AAAAIbmlzdHAyNTYAAABBBGXEEzWmm1dxb+57RoK5KVCL0w2eNv9cqJX2AGGVlkFsVDhOXHzsadS3LTK4VlEbbrDMJdoti9yM8vclA8IeRacAAAAEc3NoOg== nocomment", false)
		require.NoError(t, err)

		// Cannot push to a private repo with reading key
		results, extra = private.ServCommand(ctx, deployKey.KeyID, "user15", "big_test_private_1", perm.AccessModeWrite, "git-upload-pack", "")
		require.Error(t, extra.Error)
		assert.Empty(t, results)

		// Can pull from repo we're a writing deploy key for
		results, extra = private.ServCommand(ctx, deployKey.KeyID, "user15", "big_test_private_2", perm.AccessModeRead, "git-upload-pack", "")
		require.NoError(t, extra.Error)
		assert.False(t, results.IsWiki)
		assert.NotZero(t, results.DeployKeyID)
		assert.Equal(t, deployKey.KeyID, results.KeyID)
		assert.Equal(t, "test-deploy", results.KeyName)
		assert.Equal(t, "user15", results.UserName)
		assert.Equal(t, int64(15), results.UserID)
		assert.Equal(t, "user15", results.OwnerName)
		assert.Equal(t, "big_test_private_2", results.RepoName)
		assert.Equal(t, int64(20), results.RepoID)

		// Can push to repo we're a writing deploy key for
		results, extra = private.ServCommand(ctx, deployKey.KeyID, "user15", "big_test_private_2", perm.AccessModeWrite, "git-upload-pack", "")
		require.NoError(t, extra.Error)
		assert.False(t, results.IsWiki)
		assert.NotZero(t, results.DeployKeyID)
		assert.Equal(t, deployKey.KeyID, results.KeyID)
		assert.Equal(t, "test-deploy", results.KeyName)
		assert.Equal(t, "user15", results.UserName)
		assert.Equal(t, int64(15), results.UserID)
		assert.Equal(t, "user15", results.OwnerName)
		assert.Equal(t, "big_test_private_2", results.RepoName)
		assert.Equal(t, int64(20), results.RepoID)
	})
}

func TestAPIPrivateServAndNoServWithRequiredTwoFactor(t *testing.T) {
	onApplicationRun(t, func(*testing.T, *url.URL) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		runTest := func(t *testing.T, user *user_model.User, useTOTP, servAllowed bool) {
			t.Helper()
			repo := forgery.CreateRepository(t, user, nil)

			pubKey, err := asymkey_model.AddPublicKey(ctx, user.ID, "tmp-key-"+user.Name, "sk-ecdsa-sha2-nistp256@openssh.com AAAAInNrLWVjZHNhLXNoYTItbmlzdHAyNTZAb3BlbnNzaC5jb20AAAAIbmlzdHAyNTYAAABBBGXEEzWmm1dxb+57RoK5KVCL0w2eNv9cqJX2AGGVlkFsVDhOXHzsadS3LTK4VlEbbrDMJdoti9yM8vclA8IeRacAAAAEc3NoOg== nocomment", 0)
			require.NoError(t, err)
			defer unittest.AssertSuccessfulDelete(t, &asymkey_model.PublicKey{ID: pubKey.ID})

			if useTOTP {
				session := loginUser(t, user.Name)
				session.EnrollTOTP(t)
				session.MakeRequest(t, NewRequest(t, "POST", "/user/logout"), http.StatusOK)
				defer unittest.AssertSuccessfulDelete(t, &auth.TwoFactor{UID: user.ID})
			}

			// Can push to a repo
			_, extra := private.ServCommand(ctx, pubKey.ID, user.Name, repo.Name, perm.AccessModeWrite, "git-upload-pack", "")
			_, _, err = private.ServNoCommand(ctx, pubKey.ID)
			if servAllowed {
				require.NoError(t, extra.Error)
				require.NoError(t, err)
			} else {
				require.Error(t, extra.Error)
				require.Error(t, err)
			}
		}

		adminUser := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
		normalUser := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 4})
		restrictedUser := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 29})

		t.Run("NoneTwoFactorRequirement", func(t *testing.T) {
			// this should be the default, so don't have to set the variable

			t.Run("no 2fa", func(t *testing.T) {
				runTest(t, adminUser, false, true)
				runTest(t, normalUser, false, true)
				runTest(t, restrictedUser, false, true)
			})

			t.Run("enabled 2fa", func(t *testing.T) {
				runTest(t, adminUser, true, true)
				runTest(t, normalUser, true, true)
				runTest(t, restrictedUser, true, true)
			})
		})

		t.Run("AllTwoFactorRequirement", func(t *testing.T) {
			defer test.MockVariableValue(&setting.GlobalTwoFactorRequirement, setting.AllTwoFactorRequirement)()

			t.Run("no 2fa", func(t *testing.T) {
				runTest(t, adminUser, false, false)
				runTest(t, normalUser, false, false)
				runTest(t, restrictedUser, false, false)
			})

			t.Run("enabled 2fa", func(t *testing.T) {
				runTest(t, adminUser, true, true)
				runTest(t, normalUser, true, true)
				runTest(t, restrictedUser, true, true)
			})
		})

		t.Run("AdminTwoFactorRequirement", func(t *testing.T) {
			defer test.MockVariableValue(&setting.GlobalTwoFactorRequirement, setting.AdminTwoFactorRequirement)()

			t.Run("no 2fa", func(t *testing.T) {
				runTest(t, adminUser, false, false)
				runTest(t, normalUser, false, true)
				runTest(t, restrictedUser, false, true)
			})

			t.Run("enabled 2fa", func(t *testing.T) {
				runTest(t, adminUser, true, true)
				runTest(t, normalUser, true, true)
				runTest(t, restrictedUser, true, true)
			})
		})
	})
}
