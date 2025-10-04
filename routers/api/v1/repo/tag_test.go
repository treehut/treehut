// Copyright 2025 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package repo

import (
	"testing"

	"forgejo.org/models/unittest"
	"forgejo.org/services/contexttest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListTagsSetsLinkHeader(t *testing.T) {
	unittest.PrepareTestEnv(t)

	// limit=1 so that any repo with >=2 tags will paginate
	ctx, resp := contexttest.MockAPIContext(t, "GET /api/v1/repos/user2/repo1/tags?limit=1")
	contexttest.LoadRepo(t, ctx, 1)
	contexttest.LoadUser(t, ctx, 2)
	contexttest.LoadGitRepo(t, ctx)

	// Ensure at least two tags exist for pagination
	commit, err := ctx.Repo.GitRepo.GetBranchCommit("master")
	require.NoError(t, err)
	_ = ctx.Repo.GitRepo.CreateTag("listtags-linkheader-a", commit.ID.String())
	_ = ctx.Repo.GitRepo.CreateTag("listtags-linkheader-b", commit.ID.String())

	ListTags(ctx)

	assert.Equal(t, 200, ctx.Resp.Status())

	link := resp.Header().Get("Link")
	assert.NotEmpty(t, link, "Link header should be set for paginated responses")
	assert.Contains(t, link, "rel=\"next\"")
	assert.Contains(t, link, "page=2")
}
