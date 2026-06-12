// Copyright 2025 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package secret

import (
	"strings"
	"testing"

	"forgejo.org/models/unittest"
	"forgejo.org/modules/keying"
	"forgejo.org/modules/util"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInsertEncryptedSecret(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	t.Run("Global secret", func(t *testing.T) {
		secret, err := InsertEncryptedSecret(t.Context(), 0, 0, "GLOBAL_SECRET", "some common secret")
		require.ErrorIs(t, err, util.ErrInvalidArgument)
		assert.Nil(t, secret)
	})

	key := keying.ActionSecret

	t.Run("Insert repository secret", func(t *testing.T) {
		secret, err := InsertEncryptedSecret(t.Context(), 0, 1, "REPO_SECRET", "some repository secret")
		require.NoError(t, err)
		assert.NotNil(t, secret)
		assert.Equal(t, "REPO_SECRET", secret.Name)
		assert.EqualValues(t, 1, secret.RepoID)
		assert.NotEmpty(t, secret.Data)

		// Assert the secret is stored in the database.
		unittest.AssertExistsAndLoadBean(t, &Secret{RepoID: 1, Name: "REPO_SECRET", Data: secret.Data})

		t.Run("Keying", func(t *testing.T) {
			// Cannot decrypt with different ID.
			plainText, err := key.Decrypt(secret.Data, keying.ColumnAndID("data", secret.ID+1))
			require.Error(t, err)
			assert.Nil(t, plainText)

			// Cannot decrypt with different column.
			plainText, err = key.Decrypt(secret.Data, keying.ColumnAndID("metadata", secret.ID))
			require.Error(t, err)
			assert.Nil(t, plainText)

			// Can decrypt with correct column and ID.
			plainText, err = key.Decrypt(secret.Data, keying.ColumnAndID("data", secret.ID))
			require.NoError(t, err)
			assert.EqualValues(t, "some repository secret", plainText)
		})
	})

	t.Run("Insert owner secret", func(t *testing.T) {
		secret, err := InsertEncryptedSecret(t.Context(), 2, 0, "OWNER_SECRET", "some owner secret")
		require.NoError(t, err)
		assert.NotNil(t, secret)
		assert.Equal(t, "OWNER_SECRET", secret.Name)
		assert.EqualValues(t, 2, secret.OwnerID)
		assert.NotEmpty(t, secret.Data)

		// Assert the secret is stored in the database.
		unittest.AssertExistsAndLoadBean(t, &Secret{OwnerID: 2, Name: "OWNER_SECRET", Data: secret.Data})

		t.Run("Keying", func(t *testing.T) {
			// Cannot decrypt with different ID.
			plainText, err := key.Decrypt(secret.Data, keying.ColumnAndID("data", secret.ID+1))
			require.Error(t, err)
			assert.Nil(t, plainText)

			// Cannot decrypt with different column.
			plainText, err = key.Decrypt(secret.Data, keying.ColumnAndID("metadata", secret.ID))
			require.Error(t, err)
			assert.Nil(t, plainText)

			// Can decrypt with correct column and ID.
			plainText, err = key.Decrypt(secret.Data, keying.ColumnAndID("data", secret.ID))
			require.NoError(t, err)
			assert.EqualValues(t, "some owner secret", plainText)
		})
	})

	t.Run("Rejects invalid name", func(t *testing.T) {
		_, err := InsertEncryptedSecret(t.Context(), 2, 0, "invalid name", "some secret")
		require.ErrorContains(t, err, "invalid secret name")
	})

	t.Run("FetchActionSecrets", func(t *testing.T) {
		secrets, err := FetchActionSecrets(t.Context(), 2, 1)
		require.NoError(t, err)
		assert.Equal(t, "some owner secret", secrets["OWNER_SECRET"])
		assert.Equal(t, "some repository secret", secrets["REPO_SECRET"])
	})
}

func TestSecretDataIsNormalized(t *testing.T) {
	secret := Secret{ID: 494, OwnerID: 829, RepoID: 0, Name: "A_SECRET"}

	secret.SetData("  \r\ndatà\t  ")

	decryptedData, err := secret.GetDecryptedData()
	require.NoError(t, err)
	assert.Equal(t, "  \ndatà\t  ", decryptedData)
}

func TestSecretGetDecryptedData(t *testing.T) {
	t.Run("Recovers original data", func(t *testing.T) {
		secret := Secret{ID: 494, OwnerID: 829, RepoID: 0, Name: "A_SECRET"}
		secret.SetData("data")

		decryptedData, err := secret.GetDecryptedData()
		require.NoError(t, err)
		assert.Equal(t, "data", decryptedData)
	})

	t.Run("Returns error if data cannot be decrypted", func(t *testing.T) {
		secret := Secret{ID: 494, OwnerID: 829, RepoID: 0, Name: "A_SECRET"}
		secret.SetData("data")

		// Changing the ID without updating the secret makes the secret irrecoverable.
		secret.ID++

		decryptedData, err := secret.GetDecryptedData()
		assert.Empty(t, decryptedData)
		assert.ErrorContains(t, err, "unable to decrypt secret[id=495,name=\"A_SECRET\"]")
	})
}

func TestSecretGetSecretByID(t *testing.T) {
	defer unittest.OverrideFixtures("models/secret/TestSecretGetSecretByID")()
	require.NoError(t, unittest.PrepareTestDatabase())

	testCases := []struct {
		name          string
		ownerID       int64
		repoID        int64
		id            int64
		expectedName  string
		expectedData  string
		expectedError string
	}{
		{
			name:         "Organization secret",
			ownerID:      3,
			repoID:       0,
			id:           637340,
			expectedName: "TEST_SECRET",
			expectedData: "very secret",
		},
		{
			name:          "Owner mismatch",
			ownerID:       4,
			repoID:        0,
			id:            637340,
			expectedError: "secret with ID 637340: resource does not exist",
		},
		{
			name:          "Repository mismatch",
			ownerID:       0,
			repoID:        1,
			id:            637340,
			expectedError: "secret with ID 637340: resource does not exist",
		},
		{
			name:         "Repository secret",
			ownerID:      0,
			repoID:       62,
			id:           637341,
			expectedName: "ANOTHER_SECRET",
			expectedData: "also very secret",
		},
		{
			name:          "Unsupported instance secret",
			ownerID:       0,
			repoID:        0,
			id:            637341,
			expectedError: "ownerID and repoID cannot be simultaneously 0",
		},
		{
			name:         "User secret",
			ownerID:      1,
			repoID:       0,
			id:           637342,
			expectedName: "TEST_SECRET",
			expectedData: "super secret",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			secret, err := GetSecretByID(t.Context(), testCase.ownerID, testCase.repoID, testCase.id)

			if testCase.expectedError != "" {
				assert.ErrorContains(t, err, testCase.expectedError)
			} else {
				require.NoError(t, err)
				assert.Equal(t, testCase.id, secret.ID)
				assert.Equal(t, testCase.ownerID, secret.OwnerID)
				assert.Equal(t, testCase.repoID, secret.RepoID)
				assert.Equal(t, testCase.expectedName, secret.Name)

				data, err := secret.GetDecryptedData()
				require.NoError(t, err)
				assert.Equal(t, testCase.expectedData, data)
			}
		})
	}
}

func TestSecretUpdateSecret(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	secret, err := InsertEncryptedSecret(t.Context(), 2, 0, "a_secret", "very secret")
	require.NoError(t, err)

	secret.Name = "new_name"
	secret.SetData("also very secret")

	err = UpdateSecret(t.Context(), secret)
	require.NoError(t, err)

	updatedSecret := unittest.AssertExistsAndLoadBean(t, &Secret{ID: secret.ID})
	decryptedData, err := updatedSecret.GetDecryptedData()
	require.NoError(t, err)

	assert.Equal(t, "NEW_NAME", updatedSecret.Name)
	assert.Equal(t, "also very secret", decryptedData)
}

func TestSecretUpdateSecret_RejectsInvalidName(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	secret, err := InsertEncryptedSecret(t.Context(), 2, 0, "a_secret", "very secret")
	require.NoError(t, err)

	secret.Name = "GITHUB_IS_REJECTED" // Because it starts with `GITHUB_`.
	secret.SetData("also very secret")

	err = UpdateSecret(t.Context(), secret)
	require.ErrorContains(t, err, "invalid secret name")

	updatedSecret := unittest.AssertExistsAndLoadBean(t, &Secret{ID: secret.ID})
	decryptedData, err := updatedSecret.GetDecryptedData()
	require.NoError(t, err)

	assert.Equal(t, "A_SECRET", updatedSecret.Name)
	assert.Equal(t, "very secret", decryptedData)
}

func TestSecretValidateName(t *testing.T) {
	testCases := []struct {
		name  string
		valid bool
	}{
		{"FORGEJO_", false},
		{"PRE_FORGEJO_", true},
		{"PRE_FORGEJO_SUF", true},
		{"FORGEJO_123", false},
		{"FORGEJO_ABC", false},
		{"GITEA_", false},
		{"PRE_GITEA_", true},
		{"PRE_GITEA_SUF", true},
		{"GITEA_123", false},
		{"GITEA_ABC", false},
		{"GITHUB_", false},
		{"PRE_GITHUB_", true},
		{"PRE_GITHUB_SUF", true},
		{"GITHUB_123", false},
		{"GITHUB_ABC", false},
		{"123_TEST", false},
		{"CI", true},
		{"_CI", true},
		{"CI_", true},
		{"CI123", true},
		{"CIABC", true},
		{"FORGEJO", true},
		{"FORGEJO123", true},
		{"FORGEJOABC", true},
		{"GITEA", true},
		{"GITEA123", true},
		{"GITEAABC", true},
		{"GITHUB", true},
		{"GITHUB123", true},
		{"GITHUBABC", true},
		{"_123_TEST", true},
	}
	for _, tC := range testCases {
		t.Run(tC.name, func(t *testing.T) {
			t.Helper()
			if tC.valid {
				assert.NoError(t, ValidateName(tC.name))
				assert.NoError(t, ValidateName(strings.ToLower(tC.name)))
			} else {
				require.ErrorIs(t, ValidateName(tC.name), ErrInvalidName)
				require.ErrorIs(t, ValidateName(strings.ToLower(tC.name)), ErrInvalidName)
			}
		})
	}
}
