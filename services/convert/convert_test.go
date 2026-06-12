// Copyright 2025 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package convert

import (
	"testing"
	"time"

	actions_model "forgejo.org/models/actions"
	"forgejo.org/models/db"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/git"
	api "forgejo.org/modules/structs"
	"forgejo.org/modules/timeutil"
	"forgejo.org/modules/util"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToVerification(t *testing.T) {
	defer unittest.OverrideFixtures("models/fixtures/TestParseCommitWithSSHSignature")()
	require.NoError(t, unittest.PrepareTestDatabase())

	// Change the user's primary email address to ensure this value isn't ambiguous with any other return value from
	// signature verification.
	userModel := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	userModel.Email = "secret-email@example.com"
	db.GetEngine(t.Context()).ID(userModel.ID).Cols("email").Update(userModel)

	t.Run("SSH Key Signature", func(t *testing.T) {
		commit := &git.Commit{
			Committer: &git.Signature{
				Email: "user2@example.com",
			},
			Signature: &git.ObjectSignature{
				Payload: `tree 853694aae8816094a0d875fee7ea26278dbf5d0f
parent c2780d5c313da2a947eae22efd7dacf4213f4e7f
author user2 <user2@example.com> 1699707877 +0100
committer user2 <user2@example.com> 1699707877 +0100

Add content
`,
				Signature: `-----BEGIN SSH SIGNATURE-----
U1NIU0lHAAAAAQAAADMAAAALc3NoLWVkMjU1MTkAAAAgoGSe9Zy7Ez9bSJcaTNjh/Y7p95
f5DujjqkpzFRtw6CEAAAADZ2l0AAAAAAAAAAZzaGE1MTIAAABTAAAAC3NzaC1lZDI1NTE5
AAAAQBe2Fwk/FKY3SBCnG6jSYcO6ucyahp2SpQ/0P+otslzIHpWNW8cQ0fGLdhhaFynJXQ
fs9cMpZVM9BfIKNUSO8QY=
-----END SSH SIGNATURE-----
`,
			},
		}
		commitVerification := ToVerification(t.Context(), commit)
		require.NotNil(t, commitVerification)
		assert.Equal(t, &api.PayloadCommitVerification{
			Verified:  true,
			Reason:    "user2 / SHA256:TKfwbZMR7e9OnlV2l1prfah1TXH8CmqR0PvFEXVCXA4",
			Signature: "-----BEGIN SSH SIGNATURE-----\nU1NIU0lHAAAAAQAAADMAAAALc3NoLWVkMjU1MTkAAAAgoGSe9Zy7Ez9bSJcaTNjh/Y7p95\nf5DujjqkpzFRtw6CEAAAADZ2l0AAAAAAAAAAZzaGE1MTIAAABTAAAAC3NzaC1lZDI1NTE5\nAAAAQBe2Fwk/FKY3SBCnG6jSYcO6ucyahp2SpQ/0P+otslzIHpWNW8cQ0fGLdhhaFynJXQ\nfs9cMpZVM9BfIKNUSO8QY=\n-----END SSH SIGNATURE-----\n",
			Signer: &api.PayloadUser{
				Name:  "user2",
				Email: "user2@example.com", // expected email will match the commit's committer's email, regardless of `KeepEmailPrivate`.
			},
			Payload: "tree 853694aae8816094a0d875fee7ea26278dbf5d0f\nparent c2780d5c313da2a947eae22efd7dacf4213f4e7f\nauthor user2 <user2@example.com> 1699707877 +0100\ncommitter user2 <user2@example.com> 1699707877 +0100\n\nAdd content\n",
		}, commitVerification)
	})

	t.Run("GPG Signature", func(t *testing.T) {
		commit := &git.Commit{
			ID: git.MustIDFromString("e20aa0bcd2878f65a93de68a3eed9045d6efdd74"),
			Committer: &git.Signature{
				Email: "user2@example.com",
			},
			Signature: &git.ObjectSignature{
				Payload: `tree e20aa0bcd2878f65a93de68a3eed9045d6efdd74
parent 5cd9b9847563eb730d63d23c1f1b84868e52ae7d
author user2 <user2+committer@example.com> 1759956520 -0600
committer user2 <user2+committer@example.com> 1759956520 -0600

Add content
`,
				Signature: `-----BEGIN PGP SIGNATURE-----

iQEzBAABCgAdFiEEdlqhn25IEoMmvK5vmDaXTfEZWRMFAmjmzigACgkQmDaXTfEZ
WROC4ggAs8mD8csA6FV5e2v/4HcxuaZKCN+D8Gvku2JUigODQCA+NOX0FF2jDnCh
tXylBPB4HJw1spKkDLtOpnCUSOniBdl9NcZjnBt6sP/OSnEfLznXFra+9fCHzsu0
9uhDn3Wn1iHWXQ2ZglUwVS0ja6pNgEip8wNZBysv8+XbO1CEEW0m7zQA6tunzIwp
yiPZDUJrKtpKAK0+v19EccT2VjYAa+Vo+p3/E0piaTYNbsTqtFRy63tdjDkf+mo+
l/PaPhrMqdnbxv3/sd/63VCNdvPH3f0+OuydcC7mXyysmvap99EC+QKnpsrm7RAP
uf51WIBywxztet6vi+jYJK1jFoY4iA==
=Lnrt
-----END PGP SIGNATURE-----`,
			},
		}
		commitVerification := ToVerification(t.Context(), commit)
		require.NotNil(t, commitVerification)
		assert.Equal(t, &api.PayloadCommitVerification{
			Verified:  true,
			Reason:    "user2 / 9836974DF1195913",
			Signature: "-----BEGIN PGP SIGNATURE-----\n\niQEzBAABCgAdFiEEdlqhn25IEoMmvK5vmDaXTfEZWRMFAmjmzigACgkQmDaXTfEZ\nWROC4ggAs8mD8csA6FV5e2v/4HcxuaZKCN+D8Gvku2JUigODQCA+NOX0FF2jDnCh\ntXylBPB4HJw1spKkDLtOpnCUSOniBdl9NcZjnBt6sP/OSnEfLznXFra+9fCHzsu0\n9uhDn3Wn1iHWXQ2ZglUwVS0ja6pNgEip8wNZBysv8+XbO1CEEW0m7zQA6tunzIwp\nyiPZDUJrKtpKAK0+v19EccT2VjYAa+Vo+p3/E0piaTYNbsTqtFRy63tdjDkf+mo+\nl/PaPhrMqdnbxv3/sd/63VCNdvPH3f0+OuydcC7mXyysmvap99EC+QKnpsrm7RAP\nuf51WIBywxztet6vi+jYJK1jFoY4iA==\n=Lnrt\n-----END PGP SIGNATURE-----",
			Signer: &api.PayloadUser{
				Name:  "user2",
				Email: "user2+signingkey@example.com", // expected email will match the signing key's email
			},
			Payload: "tree e20aa0bcd2878f65a93de68a3eed9045d6efdd74\nparent 5cd9b9847563eb730d63d23c1f1b84868e52ae7d\nauthor user2 <user2+committer@example.com> 1759956520 -0600\ncommitter user2 <user2+committer@example.com> 1759956520 -0600\n\nAdd content\n",
		}, commitVerification)
	})
}

func TestToAnnotatedTag(t *testing.T) {
	defer unittest.OverrideFixtures("models/fixtures/TestParseCommitWithSSHSignature")()
	require.NoError(t, unittest.PrepareTestDatabase())

	// Align user email for predictable test results (same as TestToVerification).
	userModel := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	userModel.Email = "secret-email@example.com"
	db.GetEngine(t.Context()).ID(userModel.ID).Cols("email").Update(userModel)

	headRepo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	sha1 := git.Sha1ObjectFormat

	tagSHA := sha1.EmptyObjectID()
	commitSHA := git.MustIDFromString("e20aa0bcd2878f65a93de68a3eed9045d6efdd74")
	tagger := &git.Signature{Name: "user2", Email: "user2@example.com", When: time.Unix(1699707877, 0)}

	t.Run("Unsigned tag falls back to commit signature (GPG)", func(t *testing.T) {
		tag := &git.Tag{
			Name:    "v2.0.0",
			ID:      tagSHA,
			Object:  commitSHA,
			Type:    "commit",
			Tagger:  tagger,
			Message: "Lightweight tag\n",
			// No Signature → unsigned tag
		}

		commitPayload := `tree e20aa0bcd2878f65a93de68a3eed9045d6efdd74
parent 5cd9b9847563eb730d63d23c1f1b84868e52ae7d
author user2 <user2+committer@example.com> 1759956520 -0600
committer user2 <user2+committer@example.com> 1759956520 -0600

Add content
`
		commitGPGSig := `-----BEGIN PGP SIGNATURE-----

iQEzBAABCgAdFiEEdlqhn25IEoMmvK5vmDaXTfEZWRMFAmjmzigACgkQmDaXTfEZ
WROC4ggAs8mD8csA6FV5e2v/4HcxuaZKCN+D8Gvku2JUigODQCA+NOX0FF2jDnCh
tXylBPB4HJw1spKkDLtOpnCUSOniBdl9NcZjnBt6sP/OSnEfLznXFra+9fCHzsu0
9uhDn3Wn1iHWXQ2ZglUwVS0ja6pNgEip8wNZBysv8+XbO1CEEW0m7zQA6tunzIwp
yiPZDUJrKtpKAK0+v19EccT2VjYAa+Vo+p3/E0piaTYNbsTqtFRy63tdjDkf+mo+
l/PaPhrMqdnbxv3/sd/63VCNdvPH3f0+OuydcC7mXyysmvap99EC+QKnpsrm7RAP
uf51WIBywxztet6vi+jYJK1jFoY4iA==
=Lnrt
-----END PGP SIGNATURE-----`

		commit := &git.Commit{
			ID: commitSHA,
			Committer: &git.Signature{
				Email: "user2@example.com",
			},
			Signature: &git.ObjectSignature{
				Payload:   commitPayload,
				Signature: commitGPGSig,
			},
		}

		result, err := ToAnnotatedTag(t.Context(), nil, headRepo, tag, commit)
		require.NoError(t, err)
		require.NotNil(t, result)

		// Should fall back to commit verification (tag has no signature)
		assert.Equal(t, commitGPGSig, result.Verification.Signature, "should use the commit GPG signature")
		assert.Equal(t, commitPayload, result.Verification.Payload, "should use the commit payload")
		assert.True(t, result.Verification.Verified, "commit signature should be verified")
		assert.Equal(t, "v2.0.0", result.Tag)
		assert.Equal(t, tagSHA.String(), result.SHA)
		assert.Equal(t, util.URLJoin(headRepo.APIURL(), "git/tags", tagSHA.String()), result.URL)
	})

	t.Run("Unsigned tag, unsigned commit", func(t *testing.T) {
		tag := &git.Tag{
			Name:    "v3.0.0",
			ID:      tagSHA,
			Object:  commitSHA,
			Type:    "commit",
			Tagger:  tagger,
			Message: "No signature\n",
		}

		commit := &git.Commit{
			ID:        commitSHA,
			Committer: &git.Signature{Email: "user2@example.com"},
			// No Signature
		}

		result, err := ToAnnotatedTag(t.Context(), nil, headRepo, tag, commit)
		require.NoError(t, err)
		require.NotNil(t, result)

		assert.False(t, result.Verification.Verified, "should not be verified")
		assert.Empty(t, result.Verification.Signature, "should have no signature")
		assert.Equal(t, "v3.0.0", result.Tag)
	})
}

func TestToActionRunner(t *testing.T) {
	testCases := []struct {
		name           string
		runner         actions_model.ActionRunner
		expectedStatus api.RunnerStatus
	}{
		{
			name: "active-runner",
			runner: actions_model.ActionRunner{
				ID:          846,
				UUID:        "0bf6d33b-9be8-4bb3-a210-351ae7f3d48e",
				OwnerID:     204958,
				RepoID:      0,
				Name:        "active-example",
				Version:     "12.1.2",
				Description: "A very busy runner",
				AgentLabels: []string{"debian", "gpu"},
				LastOnline:  timeutil.TimeStampNow(),
				LastActive:  timeutil.TimeStampNow(),
			},
			expectedStatus: api.RunnerStatusActive,
		},
		{
			name: "offline-runner",
			runner: actions_model.ActionRunner{
				ID:          731,
				UUID:        "29b075f8-cd54-4dc2-b1e2-db303b32b0ce",
				OwnerID:     0,
				RepoID:      255289,
				Name:        "offline-example",
				Version:     "dev",
				Description: "",
				AgentLabels: []string{},
				LastOnline:  0,
				LastActive:  0,
			},
			expectedStatus: api.RunnerStatusOffline,
		},
		{
			name: "idle-runner",
			runner: actions_model.ActionRunner{
				ID:          117,
				UUID:        "865ca613-f258-49bc-a986-1037ace1ca35",
				OwnerID:     39115,
				RepoID:      0,
				Name:        "idle-example",
				Version:     "11.3.1",
				Description: "A runner twiddling its thumbs",
				AgentLabels: []string{"docker"},
				LastOnline:  timeutil.TimeStampNow(),
				LastActive:  timeutil.TimeStampNow().AddDuration(-actions_model.RunnerIdleTime),
			},
			expectedStatus: api.RunnerStatusIdle,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			actionRunner, err := ToActionRunner(&testCase.runner)

			require.NoError(t, err)
			assert.Equal(t, testCase.runner.ID, actionRunner.ID)
			assert.Equal(t, testCase.runner.Name, actionRunner.Name)
			assert.Equal(t, testCase.runner.UUID, actionRunner.UUID)
			assert.Equal(t, testCase.runner.OwnerID, actionRunner.OwnerID)
			assert.Equal(t, testCase.runner.RepoID, actionRunner.RepoID)
			assert.Equal(t, testCase.runner.Version, actionRunner.Version)
			assert.Equal(t, testCase.runner.Description, actionRunner.Description)
			assert.Equal(t, testCase.expectedStatus.String(), actionRunner.Status)
			assert.Equal(t, testCase.runner.AgentLabels, actionRunner.Labels)
		})
	}
}
