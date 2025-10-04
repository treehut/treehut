// Copyright 2025 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package quota_test

import (
	"testing"

	quota_model "forgejo.org/models/quota"
	"forgejo.org/models/unittest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestQuotaUsedGetUsedForUser(t *testing.T) {
	defer unittest.OverrideFixtures("models/fixtures/TestGetUsedForUser/")()
	require.NoError(t, unittest.PrepareTestDatabase())

	used, err := quota_model.GetUsedForUser(t.Context(), 5)
	require.NoError(t, err)

	assert.EqualValues(t, 4096, used.Size.Assets.Artifacts)
}

func TestQuotaUsedTotals(t *testing.T) {
	used := quota_model.Used{
		Size: quota_model.UsedSize{
			Repos: quota_model.UsedSizeRepos{
				Public:  2,
				Private: 3,
			},
			Git: quota_model.UsedSizeGit{
				LFS: 7,
			},
			Assets: quota_model.UsedSizeAssets{
				Attachments: quota_model.UsedSizeAssetsAttachments{
					Issues:   11,
					Releases: 13,
				},
				Artifacts: 17,
				Packages: quota_model.UsedSizeAssetsPackages{
					All: 19,
				},
			},
		},
	}

	assert.EqualValues(t, 5, used.Size.Repos.All())               // repos public + repos private
	assert.EqualValues(t, 12, used.Size.Git.All(used.Size.Repos)) // repos all + git lfs
	assert.EqualValues(t, 24, used.Size.Assets.Attachments.All()) // issues + releases
	assert.EqualValues(t, 60, used.Size.Assets.All())             // attachments all + artifacts + packages
	assert.EqualValues(t, 72, used.Size.All())                    // git all + assets all
}
