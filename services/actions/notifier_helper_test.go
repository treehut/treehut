// Copyright 2024 The Forgejo Authors
// SPDX-License-Identifier: MIT

package actions

import (
	"testing"

	actions_model "forgejo.org/models/actions"
	"forgejo.org/models/db"
	issues_model "forgejo.org/models/issues"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	actions_module "forgejo.org/modules/actions"
	"forgejo.org/modules/git"
	api "forgejo.org/modules/structs"
	webhook_module "forgejo.org/modules/webhook"

	"github.com/nektos/act/pkg/jobparser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_SkipPullRequestEvent(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	repoID := int64(1)
	commitSHA := "1234"

	// event is not webhook_module.HookEventPullRequestSync, never skip
	assert.False(t, SkipPullRequestEvent(db.DefaultContext, webhook_module.HookEventPullRequest, repoID, commitSHA))

	// event is webhook_module.HookEventPullRequestSync but there is nothing in the ActionRun table, do not skip
	assert.False(t, SkipPullRequestEvent(db.DefaultContext, webhook_module.HookEventPullRequestSync, repoID, commitSHA))

	// there is a webhook_module.HookEventPullRequest event but the SHA is different, do not skip
	index := int64(1)
	run := &actions_model.ActionRun{
		Index:     index,
		Event:     webhook_module.HookEventPullRequest,
		RepoID:    repoID,
		CommitSHA: "othersha",
	}
	unittest.AssertSuccessfulInsert(t, run)
	assert.False(t, SkipPullRequestEvent(db.DefaultContext, webhook_module.HookEventPullRequestSync, repoID, commitSHA))

	// there already is a webhook_module.HookEventPullRequest with the same SHA, skip
	index++
	run = &actions_model.ActionRun{
		Index:     index,
		Event:     webhook_module.HookEventPullRequest,
		RepoID:    repoID,
		CommitSHA: commitSHA,
	}
	unittest.AssertSuccessfulInsert(t, run)
	assert.True(t, SkipPullRequestEvent(db.DefaultContext, webhook_module.HookEventPullRequestSync, repoID, commitSHA))
}

func Test_IssueCommentOnForkPullRequestEvent(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 10})
	doer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
	pr := unittest.AssertExistsAndLoadBean(t, &issues_model.PullRequest{ID: 3})
	require.NoError(t, pr.LoadIssue(db.DefaultContext))

	require.True(t, pr.IsFromFork())

	commit := &git.Commit{
		ID:            git.MustIDFromString("0000000000000000000000000000000000000000"),
		CommitMessage: "test",
	}
	detectedWorkflows := []*actions_module.DetectedWorkflow{
		{
			TriggerEvent: &jobparser.Event{
				Name: "issue_comment",
			},
		},
	}
	input := &notifyInput{
		Repo:        repo,
		Doer:        doer,
		Event:       webhook_module.HookEventIssueComment,
		PullRequest: pr,
		Payload:     &api.IssueCommentPayload{},
	}

	unittest.AssertSuccessfulDelete(t, &actions_model.ActionRun{RepoID: repo.ID})

	err := handleWorkflows(db.DefaultContext, detectedWorkflows, commit, input, "")
	require.NoError(t, err)

	runs, err := db.Find[actions_model.ActionRun](db.DefaultContext, actions_model.FindRunOptions{
		RepoID: repo.ID,
	})
	require.NoError(t, err)
	require.Len(t, runs, 1)

	assert.Equal(t, webhook_module.HookEventIssueComment, runs[0].Event)
	assert.False(t, runs[0].IsForkPullRequest)
}

func Test_OpenForkPullRequestEvent(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 10})
	doer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
	pr := unittest.AssertExistsAndLoadBean(t, &issues_model.PullRequest{ID: 3})
	require.NoError(t, pr.LoadIssue(db.DefaultContext))

	require.True(t, pr.IsFromFork())

	commit := &git.Commit{
		ID:            git.MustIDFromString("0000000000000000000000000000000000000000"),
		CommitMessage: "test",
	}
	detectedWorkflows := []*actions_module.DetectedWorkflow{
		{
			TriggerEvent: &jobparser.Event{
				Name: "pull_request",
			},
		},
	}
	input := &notifyInput{
		Repo:        repo,
		Doer:        doer,
		Event:       webhook_module.HookEventPullRequest,
		PullRequest: pr,
		Payload:     &api.PullRequestPayload{},
	}

	unittest.AssertSuccessfulDelete(t, &actions_model.ActionRun{RepoID: repo.ID})

	err := handleWorkflows(db.DefaultContext, detectedWorkflows, commit, input, "")
	require.NoError(t, err)

	runs, err := db.Find[actions_model.ActionRun](db.DefaultContext, actions_model.FindRunOptions{
		RepoID: repo.ID,
	})
	require.NoError(t, err)
	require.Len(t, runs, 1)

	assert.Equal(t, webhook_module.HookEventPullRequest, runs[0].Event)
	assert.True(t, runs[0].IsForkPullRequest)
}
