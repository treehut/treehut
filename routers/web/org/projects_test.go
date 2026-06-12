// Copyright 2022 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package org_test

import (
	"net/http"
	"testing"

	issues_model "forgejo.org/models/issues"
	"forgejo.org/models/unittest"
	"forgejo.org/models/user"
	"forgejo.org/routers/web/org"
	"forgejo.org/services/contexttest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckProjectColumnChangePermissions(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx, _ := contexttest.MockContext(t, "user2/-/projects/4/4")
	contexttest.LoadUser(t, ctx, 2)
	ctx.ContextUser = ctx.Doer // user2
	ctx.SetParams(":id", "4")
	ctx.SetParams(":columnID", "4")

	project, column := org.CheckProjectColumnChangePermissions(ctx)
	assert.NotNil(t, project)
	assert.NotNil(t, column)
	assert.False(t, ctx.Written())
}

func TestViewProjectPRLinkVisibility(t *testing.T) {
	defer unittest.OverrideFixtures("routers/web/org/TestViewProjectPRLinkVisibility")()
	unittest.PrepareTestEnv(t)

	// When a private PR references a public issue, and the public issue is put onto a project board, then the
	// cross-reference from the private PR to the public issue should only be visible if the private PR is visible to
	// the logged-in user.
	//
	// Test requires data fixtures:
	//
	// private repo, w/ PR in the private repo
	//     repository.id = 3, org3/repo3
	//     PR is issue.ID = 12
	// public repo, public issue
	//     repository.id = 32, org3/repo21
	//     issue.id = 16, org3/repo21#1
	// cross-ref comment from the private PR -> public issue
	//     comment.id = 3000
	// project board
	//     project.id = 7, owned by org3
	// put public issue on the project board
	//     project_issue.id = 10

	test := func(loggedIn bool) map[int64][]*issues_model.Issue {
		ctx, recorder := contexttest.MockContext(t, "org3/-/projects/7")
		if loggedIn {
			contexttest.LoadUser(t, ctx, 2)
		}
		contexttest.LoadOrganization(t, ctx, 3)
		ctx.ContextUser = unittest.AssertExistsAndLoadBean(t, &user.User{ID: 3})
		ctx.SetParams(":id", "7")

		org.ViewProject(ctx)
		assert.Equal(t, http.StatusOK, recorder.Result().StatusCode) // Verify it's a success response

		// Map issue on the project (16) to array of PRs that reference that issue.
		linkedPRs, ok := ctx.Data["LinkedPRs"].(map[int64][]*issues_model.Issue)
		require.True(t, ok, "LinkedPRs must be map[int64][]*issues_model.Issue")

		return linkedPRs
	}

	t.Run("authorized user with visibility", func(t *testing.T) {
		linkedPRs := test(true)

		prList, ok := linkedPRs[16]
		require.True(t, ok, "linkedPRs must contain ID=16")
		assert.Len(t, prList, 1)

		prIssue := prList[0]
		assert.True(t, prIssue.IsPull)
		assert.EqualValues(t, 12, prIssue.ID)
	})

	t.Run("anonymous user", func(t *testing.T) {
		linkedPRs := test(false)

		prList, ok := linkedPRs[16]
		require.True(t, ok, "linkedPRs must contain ID=16")
		assert.Empty(t, prList)
	})
}
