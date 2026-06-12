// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package stats

import (
	"testing"
	"time"

	"forgejo.org/models/db"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unittest"
	"forgejo.org/modules/queue"
	"forgejo.org/modules/setting"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	unittest.MainTest(m)
}

func TestRepoStatsIndex(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	setting.CfgProvider, _ = setting.NewConfigProviderFromData("")

	setting.LoadQueueSettings()

	err := Init()
	require.NoError(t, err)

	repo, err := repo_model.GetRepositoryByID(db.DefaultContext, 1)
	require.NoError(t, err)

	err = UpdateRepoIndexer(repo)
	require.NoError(t, err)

	require.NoError(t, queue.GetManager().FlushAll(t.Context(), 5*time.Second))

	status, err := repo_model.GetIndexerStatus(db.DefaultContext, repo, repo_model.RepoIndexerTypeStats)
	require.NoError(t, err)
	assert.Equal(t, "65f1bf27bc3bf70f64657658635e66094edbcb4d", status.CommitSha)
	langs, err := repo_model.GetTopLanguageStats(db.DefaultContext, repo, 5)
	require.NoError(t, err)
	if assert.Len(t, langs, 1) {
		assert.Equal(t, &repo_model.LanguageStat{ID: 1, RepoID: 1, CommitID: "65f1bf27bc3bf70f64657658635e66094edbcb4d", IsPrimary: true, Language: "Go", Percentage: 99.9, Size: 1023, Color: "#00ADD8", CreatedUnix: 1779733547}, langs[0])
	}
}
