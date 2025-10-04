// Copyright 2025 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT
package redirect

import (
	"testing"

	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLookupUserRedirect(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	adminUser := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
	normalUser := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})

	testOk := func(t *testing.T, doer *user_model.User, username string, expectedRedirectID int64) {
		t.Helper()

		redirectID, err := LookupUserRedirect(t.Context(), doer, username)
		require.NoError(t, err)
		assert.Equal(t, expectedRedirectID, redirectID)
	}

	testFail := func(t *testing.T, doer *user_model.User, username string) {
		t.Helper()

		redirectID, err := LookupUserRedirect(t.Context(), doer, username)
		require.ErrorIs(t, err, user_model.ErrUserRedirectNotExist{Name: username, MissingPermission: true})
		assert.Zero(t, redirectID)
	}

	t.Run("Public visibility", func(t *testing.T) {
		username := "olduser1"
		redirectID := int64(1)

		testOk(t, nil, username, redirectID)
		testOk(t, normalUser, username, redirectID)
		testOk(t, adminUser, username, redirectID)
	})

	t.Run("Limited visibility", func(t *testing.T) {
		username := "oldorg22"
		redirectID := int64(22)

		testFail(t, nil, username)
		testOk(t, normalUser, username, redirectID)
		testOk(t, adminUser, username, redirectID)
	})

	t.Run("Private visibility", func(t *testing.T) {
		orgUser := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 5})
		username := "oldorg23"
		redirectID := int64(23)

		testFail(t, nil, username)
		testFail(t, normalUser, username)
		testOk(t, orgUser, username, redirectID)
		testOk(t, adminUser, username, redirectID)
	})
}
