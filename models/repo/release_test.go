// Copyright 2023 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package repo

import (
	"testing"

	"forgejo.org/models/db"
	"forgejo.org/models/unittest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMigrate_InsertReleases(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	a := &Attachment{
		UUID: "a0eebc91-9c0c-4ef7-bb6e-6bb9bd380a12",
	}
	r := &Release{
		RepoID:      1001,
		Attachments: []*Attachment{a},
	}

	err := InsertReleases(db.DefaultContext, r)
	require.NoError(t, err)

	assert.EqualValues(t, 1001, unittest.AssertExistsAndLoadBean(t, &Attachment{UUID: "a0eebc91-9c0c-4ef7-bb6e-6bb9bd380a12"}).RepoID)
}

func TestReleaseLoadRepo(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	release := unittest.AssertExistsAndLoadBean(t, &Release{ID: 1})
	assert.Nil(t, release.Repo)

	require.NoError(t, release.LoadRepo(db.DefaultContext))

	assert.EqualValues(t, 1, release.Repo.ID)
}

func TestReleaseDisplayName(t *testing.T) {
	release := Release{TagName: "TagName"}

	assert.Empty(t, release.DisplayName())

	release.IsTag = true
	assert.Equal(t, "TagName", release.DisplayName())

	release.Title = "Title"
	assert.Equal(t, "Title", release.DisplayName())
}

func Test_FindTagsByCommitIDs(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	sha1Rels, err := FindTagsByCommitIDs(db.DefaultContext, 1, "65f1bf27bc3bf70f64657658635e66094edbcb4d")
	require.NoError(t, err)
	assert.Len(t, sha1Rels, 1)
	rels := sha1Rels["65f1bf27bc3bf70f64657658635e66094edbcb4d"]
	assert.Len(t, rels, 3)
	assert.Equal(t, "v1.1", rels[0].TagName)
	assert.Equal(t, "delete-tag", rels[1].TagName)
	assert.Equal(t, "v1.0", rels[2].TagName)
}
