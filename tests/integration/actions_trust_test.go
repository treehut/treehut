// Copyright 2025 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package integration

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	actions_model "forgejo.org/models/actions"
	auth_model "forgejo.org/models/auth"
	issues_model "forgejo.org/models/issues"
	repo_model "forgejo.org/models/repo"
	unit_model "forgejo.org/models/unit"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	actions_module "forgejo.org/modules/actions"
	"forgejo.org/modules/git"
	"forgejo.org/modules/structs"
	"forgejo.org/modules/translation"
	actions_service "forgejo.org/services/actions"
	pull_service "forgejo.org/services/pull"
	repo_service "forgejo.org/services/repository"
	files_service "forgejo.org/services/repository/files"
	"forgejo.org/tests"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func actionsTrustTestClickTrustPanel(t *testing.T, session *TestSession, url, trust string) {
	// an admin approves the run once
	req := NewRequest(t, "GET", url)
	resp := session.MakeRequest(t, req, http.StatusOK)
	htmlDoc := NewHTMLParser(t, resp.Body)

	htmlDoc.AssertElement(t, "#pull-request-trust-panel", true)
	link, exists := htmlDoc.doc.Find("#pull-request-trust-panel-" + trust).Attr("action")
	require.True(t, exists)
	actualTrust, exists := htmlDoc.doc.Find(fmt.Sprintf("#pull-request-trust-panel-%s input[name='trust']", trust)).Attr("value")
	require.True(t, exists)
	require.Equal(t, trust, actualTrust)
	req = NewRequestWithValues(t, "POST", link, map[string]string{
		"trust": trust,
	})
	session.MakeRequest(t, req, http.StatusSeeOther)
}

func actionsTrustTestAssertTrustPanelPresence(t *testing.T, session *TestSession, url string, present bool) {
	t.Helper()
	req := NewRequest(t, "GET", url)
	resp := session.MakeRequest(t, req, http.StatusOK)
	htmlDoc := NewHTMLParser(t, resp.Body)

	htmlDoc.AssertElement(t, ".error-code", false)
	htmlDoc.AssertElement(t, "#pull-request-trust-panel", present)
}

func actionsTrustTestAssertTrustPanel(t *testing.T, session *TestSession, url string) {
	t.Helper()
	actionsTrustTestAssertTrustPanelPresence(t, session, url, true)
}

func actionsTrustTestAssertNoTrustPanel(t *testing.T, session *TestSession, url string) {
	t.Helper()
	actionsTrustTestAssertTrustPanelPresence(t, session, url, false)
}

func actionsTrustTestAssertPRIsWIP(t *testing.T, session *TestSession, url string) {
	t.Helper()

	req := NewRequest(t, "GET", url)
	resp := session.MakeRequest(t, req, http.StatusOK)
	htmlDoc := NewHTMLParser(t, resp.Body)

	locale := translation.NewLocale("en-US")
	assert.Equal(t, 1, htmlDoc.FindByTextTrim("div", locale.TrString("repo.pulls.cannot_merge_work_in_progress")).Length())
}

func actionsTrustTestAssertPRConflicted(t *testing.T, session *TestSession, url string) {
	t.Helper()

	req := NewRequest(t, "GET", url)
	resp := session.MakeRequest(t, req, http.StatusOK)
	htmlDoc := NewHTMLParser(t, resp.Body)

	locale := translation.NewLocale("en-US")

	// ....Eventually is used because conflict checking is async and may not complete immediately.
	require.Eventually(t, func() bool {
		return htmlDoc.FindByTextTrim("div", locale.TrString("repo.pulls.files_conflicted")).Length() == 1
	}, 5*time.Second, time.Millisecond*100)
}

func actionsTrustTestCreateBaseRepo(t *testing.T, owner *user_model.User) (*repo_model.Repository, func()) {
	t.Helper()

	// create the base repo
	baseRepo, _, f := tests.CreateDeclarativeRepo(t, owner, "repo-pull-request",
		[]unit_model.Type{unit_model.TypeActions}, nil, nil,
	)

	// add workflow file to the base repo
	addWorkflowToBaseResp, err := files_service.ChangeRepoFiles(git.DefaultContext, baseRepo, owner, &files_service.ChangeRepoFilesOptions{
		Files: []*files_service.ChangeRepoFile{
			{
				Operation: "create",
				TreePath:  ".forgejo/workflows/pr.yml",
				ContentReader: strings.NewReader(`
on:
  pull_request:
    types: [edited, opened, synchronize, reopened]
jobs:
  test:
    runs-on: docker
    steps:
      - run: echo helloworld
`),
			},
		},
		Message:   "add workflow",
		OldBranch: "main",
		NewBranch: "main",
		Author: &files_service.IdentityOptions{
			Name:  owner.Name,
			Email: owner.Email,
		},
		Committer: &files_service.IdentityOptions{
			Name:  owner.Name,
			Email: owner.Email,
		},
		Dates: &files_service.CommitDateOptions{
			Author:    time.Now(),
			Committer: time.Now(),
		},
	})
	require.NoError(t, err)
	require.NotEmpty(t, addWorkflowToBaseResp)
	return baseRepo, f
}

func actionsTrustTestRequireRun(t *testing.T, repo *repo_model.Repository, modifiedFiles *structs.FilesResponse) {
	t.Helper()

	actionRun := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{RepoID: repo.ID, CommitSHA: modifiedFiles.Commit.SHA})
	require.Equal(t, actions_module.GithubEventPullRequest, actionRun.TriggerEvent)
	require.Equal(t, actions_model.StatusWaiting.String(), actionRun.Status.String())
	unittest.BeanExists(t, &actions_model.ActionRunJob{RunID: actionRun.ID, RepoID: repo.ID})
}

func actionsTrustTestRepoCreateBranch(t *testing.T, doer *user_model.User, repo *repo_model.Repository) *structs.FilesResponse {
	t.Helper()

	return actionsTrustTestModifyRepo(t, doer, repo, "file_in_fork.txt", "main", "fork-branch-1", "content")
}

func actionsTrustMakePRConflicted(t *testing.T, doer *user_model.User, repo *repo_model.Repository) *structs.FilesResponse {
	t.Helper()

	return actionsTrustTestModifyRepo(t, doer, repo, "file_in_fork.txt", "main", "main", "conflicting content")
}

func actionsTrustTestRepoModify(t *testing.T, doer *user_model.User, baseRepo, headRepo *repo_model.Repository, filename string) *structs.FilesResponse {
	t.Helper()

	modified := actionsTrustTestModifyRepo(t, doer, headRepo, filename, "fork-branch-1", "fork-branch-1", "content")
	// the creation of the run is not synchronous
	require.Eventually(t, func() bool {
		return unittest.BeanExists(t, &actions_model.ActionRun{RepoID: baseRepo.ID, CommitSHA: modified.Commit.SHA})
	}, 60*time.Second, time.Millisecond*100)
	return modified
}

func actionsTrustTestModifyRepo(t *testing.T, doer *user_model.User, repo *repo_model.Repository, filename, oldBranch, newBranch, content string) *structs.FilesResponse {
	t.Helper()

	// add a new file to the forked repo
	addFile, err := files_service.ChangeRepoFiles(git.DefaultContext, repo, doer, &files_service.ChangeRepoFilesOptions{
		Files: []*files_service.ChangeRepoFile{
			{
				Operation:     "create",
				TreePath:      filename,
				ContentReader: strings.NewReader(content),
			},
		},
		Message:   "add " + filename,
		OldBranch: oldBranch,
		NewBranch: newBranch,
		Author: &files_service.IdentityOptions{
			Name:  doer.Name,
			Email: doer.Email,
		},
		Committer: &files_service.IdentityOptions{
			Name:  doer.Name,
			Email: doer.Email,
		},
		Dates: &files_service.CommitDateOptions{
			Author:    time.Now(),
			Committer: time.Now(),
		},
	})
	require.NoError(t, err)
	require.NotEmpty(t, addFile)
	return addFile
}

func actionsTrustTestCreatePullRequestFromForkedRepo(t *testing.T, baseUser *user_model.User, baseRepo *repo_model.Repository, headUser *user_model.User) (*repo_model.Repository, *issues_model.PullRequest, *structs.FilesResponse) {
	t.Helper()

	forkRepo := func(t *testing.T, baseUser *user_model.User, baseRepo *repo_model.Repository, headUser *user_model.User) *repo_model.Repository {
		t.Helper()

		// create the forked repo
		forkedRepo, err := repo_service.ForkRepositoryAndUpdates(git.DefaultContext, baseUser, headUser, repo_service.ForkRepoOptions{
			BaseRepo:    baseRepo,
			Name:        "forked-repo-pull-request",
			Description: "test pull-request event",
		})
		require.NoError(t, err)
		require.NotEmpty(t, forkedRepo)
		return forkedRepo
	}

	forkedRepo := forkRepo(t, baseUser, baseRepo, headUser)
	addFileToForkedResp := actionsTrustTestRepoCreateBranch(t, headUser, forkedRepo)

	// create Pull
	pullIssue := &issues_model.Issue{
		RepoID:   baseRepo.ID,
		Title:    "Test pull-request",
		PosterID: headUser.ID,
		Poster:   headUser,
		IsPull:   true,
	}
	pullRequest := &issues_model.PullRequest{
		HeadRepoID: forkedRepo.ID,
		BaseRepoID: baseRepo.ID,
		HeadBranch: "fork-branch-1",
		BaseBranch: "main",
		HeadRepo:   forkedRepo,
		BaseRepo:   baseRepo,
		Type:       issues_model.PullRequestGitea,
	}
	// create the pull request
	err := pull_service.NewPullRequest(git.DefaultContext, baseRepo, pullIssue, nil, nil, pullRequest, nil)
	require.NoError(t, err)

	actionsTrustTestRequireRun(t, baseRepo, addFileToForkedResp)

	return forkedRepo, pullRequest, addFileToForkedResp
}

func actionsTrustSetCollaborator(t *testing.T, token string, repo *repo_model.Repository, user *user_model.User, permission string) {
	t.Helper()

	repoAPILink := repo.APIURL()
	req := NewRequestWithJSON(t, "PUT", fmt.Sprintf("%s/collaborators/%s", repoAPILink, user.Name), map[string]string{"permission": permission}).AddTokenAuth(token)
	MakeRequest(t, req, http.StatusNoContent)
}

// Mark the PR as a work-in-progress PR
func actionsTrustTestSetPullRequestWIP(t *testing.T, pullRequest *issues_model.PullRequest, wip bool) {
	t.Helper()
	newTitle := pullRequest.Issue.Title
	if wip && !pullRequest.IsWorkInProgress(t.Context()) {
		newTitle = fmt.Sprintf("WIP: %s", pullRequest.Issue.Title)
	} else if !wip {
		prefix := pullRequest.GetWorkInProgressPrefix(t.Context())
		newTitle = pullRequest.Issue.Title[len(prefix):]
	}
	pullRequest.Issue.Title = newTitle
	require.NoError(t, issues_model.UpdateIssueCols(t.Context(), pullRequest.Issue, "name"))

	pullRequest.Issue = nil
	require.NoError(t, pullRequest.LoadIssue(t.Context()))
}

func actionsTrustTestModifyTitlePullRequest(t *testing.T, token string, pullRequest *issues_model.PullRequest, sha string) {
	t.Helper()

	prAPILink := pullRequest.Issue.APIURL(t.Context())
	req := NewRequestWithJSON(t, "PATCH", prAPILink, map[string]string{"title": "modified title"}).AddTokenAuth(token)
	MakeRequest(t, req, http.StatusCreated)

	actionRun := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{RepoID: pullRequest.BaseRepoID, CommitSHA: sha}, "need_approval = true")
	assert.True(t, actionRun.NeedApproval)
	actionRunJob := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{RunID: actionRun.ID, RepoID: pullRequest.BaseRepoID})
	assert.Equal(t, actions_model.StatusBlocked, actionRunJob.Status)
}

func TestActionsPullRequestTrustPanelImplicit(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		ownerUser := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2}) // owner of the repo
		ownerSession := loginUser(t, ownerUser.Name)
		ownerToken := getTokenForLoggedInUser(t, ownerSession, auth_model.AccessTokenScopeAll)

		regularUser := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 5}) // a regular user with no specific permission
		regularSession := loginUser(t, regularUser.Name)

		baseRepo, f := actionsTrustTestCreateBaseRepo(t, ownerUser)
		defer f()

		_, pullRequest, _ := actionsTrustTestCreatePullRequestFromForkedRepo(t, ownerUser, baseRepo, regularUser)
		pullRequestLink := pullRequest.Issue.Link()

		t.Run("All users see a pending approval on a newly created pull request from a fork", func(t *testing.T) {
			actionsTrustTestAssertTrustPanel(t, regularSession, pullRequestLink)
			actionsTrustTestAssertTrustPanel(t, ownerSession, pullRequestLink)
		})

		t.Run("The regular user becomes implicitly trusted and the pending approval are still displayed because they were created when the user was not trusted", func(t *testing.T) {
			actionsTrustSetCollaborator(t, ownerToken, baseRepo, regularUser, "admin")
			actionsTrustTestAssertTrustPanel(t, regularSession, pullRequestLink)
			actionsTrustTestAssertTrustPanel(t, ownerSession, pullRequestLink)
		})

		t.Run("The newly implicitly trusted user can approve its own runs", func(t *testing.T) {
			actionsTrustTestClickTrustPanel(t, regularSession, pullRequestLink, string(actions_service.UserTrustedOnce))
			actionsTrustTestAssertNoTrustPanel(t, regularSession, pullRequestLink)
			actionsTrustTestAssertNoTrustPanel(t, ownerSession, pullRequestLink)
		})
	})
}

func TestActionsPullRequestTrustPanelExplicit(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		ownerUser := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2}) // owner of the repo

		regularUser := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 5}) // a regular user with no specific permission
		regularSession := loginUser(t, regularUser.Name)

		userAdmin := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1}) // the instance admin
		adminSession := loginUser(t, userAdmin.Name)

		baseRepo, f := actionsTrustTestCreateBaseRepo(t, ownerUser)
		defer f()

		forkedRepo, pullRequest, addFileToForkedResp := actionsTrustTestCreatePullRequestFromForkedRepo(t, ownerUser, baseRepo, regularUser)
		pullRequestLink := pullRequest.Issue.Link()

		t.Run("Regular user sees a pending approval on a newly created pull request from a fork", func(t *testing.T) {
			actionsTrustTestAssertTrustPanel(t, regularSession, pullRequestLink)
		})

		t.Run("Admin approves runs once", func(t *testing.T) {
			actionRun := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{RepoID: baseRepo.ID, CommitSHA: addFileToForkedResp.Commit.SHA})

			{
				actionRun := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{RepoID: baseRepo.ID, CommitSHA: addFileToForkedResp.Commit.SHA})
				assert.True(t, actionRun.NeedApproval)
				actionRunJob := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{RunID: actionRun.ID, RepoID: baseRepo.ID})
				assert.Equal(t, actions_model.StatusBlocked.String(), actionRunJob.Status.String())
			}

			actionsTrustTestAssertTrustPanel(t, adminSession, pullRequestLink)
			actionsTrustTestClickTrustPanel(t, adminSession, pullRequestLink, string(actions_service.UserTrustedOnce))

			{
				actionRunJob := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{RunID: actionRun.ID, RepoID: baseRepo.ID})
				assert.Equal(t, actions_model.StatusWaiting.String(), actionRunJob.Status.String())
				actionRun := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{RepoID: baseRepo.ID})
				assert.False(t, actionRun.NeedApproval)
			}
		})

		t.Run("All users sees no pending approval because it was approved once", func(t *testing.T) {
			actionsTrustTestAssertNoTrustPanel(t, regularSession, pullRequestLink)
			actionsTrustTestAssertNoTrustPanel(t, adminSession, pullRequestLink)
		})

		modifiedForkedResp := actionsTrustTestRepoModify(t, regularUser, baseRepo, forkedRepo, "add_file_one.txt")

		t.Run("Regular user sees a pending approval on a modified pull request from a fork (2)", func(t *testing.T) {
			actionsTrustTestAssertTrustPanel(t, regularSession, pullRequestLink)
		})

		t.Run("Admin denies runs", func(t *testing.T) {
			actionRun := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{RepoID: baseRepo.ID, CommitSHA: modifiedForkedResp.Commit.SHA})

			{
				actionRun := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{RepoID: baseRepo.ID, CommitSHA: modifiedForkedResp.Commit.SHA})
				assert.True(t, actionRun.NeedApproval)
				actionRunJob := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{RunID: actionRun.ID, RepoID: baseRepo.ID})
				assert.Equal(t, actions_model.StatusBlocked.String(), actionRunJob.Status.String())
			}

			actionsTrustTestAssertTrustPanel(t, adminSession, pullRequestLink)
			actionsTrustTestClickTrustPanel(t, adminSession, pullRequestLink, string(actions_service.UserTrustDenied))

			{
				actionRunJob := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{RunID: actionRun.ID, RepoID: baseRepo.ID})
				assert.Equal(t, actions_model.StatusCancelled.String(), actionRunJob.Status.String())
				actionRun := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{RepoID: baseRepo.ID})
				assert.False(t, actionRun.NeedApproval)
			}
		})

		t.Run("All users sees no pending approval because it was denied", func(t *testing.T) {
			actionsTrustTestAssertNoTrustPanel(t, regularSession, pullRequestLink)
			actionsTrustTestAssertNoTrustPanel(t, adminSession, pullRequestLink)
		})

		modifiedForkedResp = actionsTrustTestRepoModify(t, regularUser, baseRepo, forkedRepo, "add_file_two.txt")

		t.Run("Regular user sees a pending approval on a modified pull request from a fork (2)", func(t *testing.T) {
			actionsTrustTestAssertTrustPanel(t, regularSession, pullRequestLink)
		})

		t.Run("Admin always trusts the poster", func(t *testing.T) {
			actionRun := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{RepoID: baseRepo.ID, CommitSHA: modifiedForkedResp.Commit.SHA})

			{
				actionRun := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{RepoID: baseRepo.ID, CommitSHA: modifiedForkedResp.Commit.SHA})
				assert.True(t, actionRun.NeedApproval)
				actionRunJob := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{RunID: actionRun.ID, RepoID: baseRepo.ID})
				assert.Equal(t, actions_model.StatusBlocked.String(), actionRunJob.Status.String())
			}

			actionsTrustTestAssertTrustPanel(t, adminSession, pullRequestLink)
			actionsTrustTestClickTrustPanel(t, adminSession, pullRequestLink, string(actions_service.UserAlwaysTrusted))

			{
				actionRunJob := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{RunID: actionRun.ID, RepoID: baseRepo.ID})
				assert.Equal(t, actions_model.StatusWaiting.String(), actionRunJob.Status.String())
				actionRun := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{RepoID: baseRepo.ID})
				assert.False(t, actionRun.NeedApproval)
			}
		})

		t.Run("Regular users sees no pending approval because it was approved", func(t *testing.T) {
			actionsTrustTestAssertNoTrustPanel(t, regularSession, pullRequestLink)
		})

		modifiedForkedResp = actionsTrustTestRepoModify(t, regularUser, baseRepo, forkedRepo, "add_file_three.txt")

		t.Run("No need for approval because the poster is always trusted", func(t *testing.T) {
			actionRun := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{RepoID: baseRepo.ID, CommitSHA: modifiedForkedResp.Commit.SHA})
			assert.False(t, actionRun.NeedApproval)
			actionRunJob := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{RunID: actionRun.ID, RepoID: baseRepo.ID})
			assert.Equal(t, actions_model.StatusWaiting.String(), actionRunJob.Status.String())
		})

		t.Run("Admin revokes the trusted poster", func(t *testing.T) {
			actionsTrustTestAssertTrustPanel(t, adminSession, pullRequestLink)
			actionsTrustTestClickTrustPanel(t, adminSession, pullRequestLink, string(actions_service.UserTrustRevoked))
		})

		modifiedForkedResp = actionsTrustTestRepoModify(t, regularUser, baseRepo, forkedRepo, "add_file_four.txt")

		t.Run("There needs to be an approval again because the user is no longer trusted", func(t *testing.T) {
			actionRun := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{RepoID: baseRepo.ID, CommitSHA: modifiedForkedResp.Commit.SHA})
			assert.True(t, actionRun.NeedApproval)
			actionRunJob := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{RunID: actionRun.ID, RepoID: baseRepo.ID})
			assert.Equal(t, actions_model.StatusBlocked.String(), actionRunJob.Status.String())
		})
	})
}

func TestActionsPullRequestTrustPanelWIPConflicts(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		ownerUser := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2}) // owner of the repo

		regularUser := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 5}) // a regular user with no specific permission
		regularSession := loginUser(t, regularUser.Name)

		userAdmin := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1}) // the instance admin
		adminSession := loginUser(t, userAdmin.Name)

		baseRepo, f := actionsTrustTestCreateBaseRepo(t, ownerUser)
		defer f()

		_, pullRequest, _ := actionsTrustTestCreatePullRequestFromForkedRepo(t, ownerUser, baseRepo, regularUser)
		pullRequestLink := pullRequest.Issue.Link()

		actionsTrustTestSetPullRequestWIP(t, pullRequest, true)
		actionsTrustTestAssertPRIsWIP(t, adminSession, pullRequestLink)

		t.Run("Regular user sees pending approval even though PR is a WIP PR", func(t *testing.T) {
			actionsTrustTestAssertTrustPanel(t, regularSession, pullRequestLink)
		})

		actionsTrustTestSetPullRequestWIP(t, pullRequest, false)
		_ = actionsTrustMakePRConflicted(t, userAdmin, baseRepo)
		actionsTrustTestAssertPRConflicted(t, adminSession, pullRequestLink)

		t.Run("Regular user sees pending approval even though PR is conflicted", func(t *testing.T) {
			actionsTrustTestAssertTrustPanel(t, regularSession, pullRequestLink)
		})
	})
}

func actionsTrustTestMergePullRequest(t *testing.T, token string, pullRequest *issues_model.PullRequest) {
	t.Helper()

	mergeURL := fmt.Sprintf("%s/pulls/%d/merge", pullRequest.Issue.Repo.APIURL(), pullRequest.Issue.Index)
	req := NewRequestWithJSON(t, "POST", mergeURL, map[string]string{"do": "merge"}).AddTokenAuth(token)
	MakeRequest(t, req, http.StatusOK)

	pr := unittest.AssertExistsAndLoadBean(t, &issues_model.PullRequest{ID: pullRequest.ID})
	assert.True(t, pr.HasMerged)
}

func TestActionsPullRequestTrustPanelMergedOrClosed(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		ownerUser := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2}) // owner of the repo

		regularUser := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 5}) // a regular user with no specific permission
		regularSession := loginUser(t, regularUser.Name)
		regularToken := getTokenForLoggedInUser(t, regularSession, auth_model.AccessTokenScopeAll)

		userAdmin := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1}) // the instance admin
		adminSession := loginUser(t, userAdmin.Name)
		adminToken := getTokenForLoggedInUser(t, adminSession, auth_model.AccessTokenScopeAll)

		baseRepo, f := actionsTrustTestCreateBaseRepo(t, ownerUser)
		defer f()

		_, pullRequest, addFileToForkedResp := actionsTrustTestCreatePullRequestFromForkedRepo(t, ownerUser, baseRepo, regularUser)
		pullRequestLink := pullRequest.Issue.Link()

		actionsTrustTestMergePullRequest(t, adminToken, pullRequest)
		actionsTrustTestModifyTitlePullRequest(t, regularToken, pullRequest, addFileToForkedResp.Commit.SHA)

		t.Run("Regular user sees pending approval even though PR is a merged PR", func(t *testing.T) {
			actionsTrustTestAssertTrustPanel(t, regularSession, pullRequestLink)
		})
		t.Run("Owner user sees pending approval even though PR is a merged PR", func(t *testing.T) {
			ownerSession := loginUser(t, ownerUser.Name)
			actionsTrustTestAssertTrustPanel(t, ownerSession, pullRequestLink)
		})
	})
}

func actionsTrustTestClosePullRequest(t *testing.T, token string, pullRequest *issues_model.PullRequest) {
	t.Helper()

	prAPILink := pullRequest.Issue.APIURL(t.Context())
	req := NewRequestWithJSON(t, "PATCH", prAPILink, map[string]string{"state": "closed"}).AddTokenAuth(token)
	MakeRequest(t, req, http.StatusCreated)

	pr := unittest.AssertExistsAndLoadBean(t, &issues_model.PullRequest{ID: pullRequest.ID})
	require.NoError(t, pr.LoadIssue(t.Context()))
	assert.True(t, pr.Issue.IsClosed)
}

func TestActionsPullRequestTrustPanelClosed(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		ownerUser := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2}) // owner of the repo

		regularUser := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 5}) // a regular user with no specific permission
		regularSession := loginUser(t, regularUser.Name)
		regularToken := getTokenForLoggedInUser(t, regularSession, auth_model.AccessTokenScopeAll)

		userAdmin := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1}) // the instance admin
		adminSession := loginUser(t, userAdmin.Name)
		adminToken := getTokenForLoggedInUser(t, adminSession, auth_model.AccessTokenScopeAll)

		baseRepo, f := actionsTrustTestCreateBaseRepo(t, ownerUser)
		defer f()

		_, pullRequest, addFileToForkedResp := actionsTrustTestCreatePullRequestFromForkedRepo(t, ownerUser, baseRepo, regularUser)
		pullRequestLink := pullRequest.Issue.Link()

		actionsTrustTestClosePullRequest(t, adminToken, pullRequest)
		actionsTrustTestModifyTitlePullRequest(t, regularToken, pullRequest, addFileToForkedResp.Commit.SHA)

		t.Run("Regular user sees pending approval even though PR is a closed PR", func(t *testing.T) {
			actionsTrustTestAssertTrustPanel(t, regularSession, pullRequestLink)
		})
	})
}

func TestActionsPullRequestTrustCancelOnClose(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		ownerUser := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})

		regularUser := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 5})
		regularSession := loginUser(t, regularUser.Name)
		token := getTokenForLoggedInUser(t, regularSession, auth_model.AccessTokenScopeWriteIssue)

		baseRepo, f := actionsTrustTestCreateBaseRepo(t, ownerUser)
		defer f()

		_, pullRequest, addFileToForkedResp := actionsTrustTestCreatePullRequestFromForkedRepo(t, ownerUser, baseRepo, regularUser)
		prAPILink := pullRequest.Issue.APIURL(t.Context())

		{
			actionRun := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{RepoID: baseRepo.ID, CommitSHA: addFileToForkedResp.Commit.SHA})
			assert.True(t, actionRun.NeedApproval)
			actionRunJob := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{RunID: actionRun.ID, RepoID: baseRepo.ID})
			assert.Equal(t, actions_model.StatusBlocked, actionRunJob.Status)
		}

		req := NewRequestWithJSON(t, "PATCH", prAPILink, &structs.PullRequest{State: "closed"}).AddTokenAuth(token)
		MakeRequest(t, req, http.StatusCreated)

		{
			actionRun := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{RepoID: baseRepo.ID, CommitSHA: addFileToForkedResp.Commit.SHA})
			assert.False(t, actionRun.NeedApproval)
			assert.Equal(t, actions_model.StatusCancelled, actionRun.Status)
			actionRunJob := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{RunID: actionRun.ID, RepoID: baseRepo.ID})
			assert.Equal(t, actions_model.StatusCancelled, actionRunJob.Status)
		}
	})
}

func TestActionsPullRequestTrustPushCancel(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		ownerUser := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
		ownerSession := loginUser(t, ownerUser.Name)

		regularUser := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 5})

		baseRepo, f := actionsTrustTestCreateBaseRepo(t, ownerUser)
		defer f()

		forkedRepo, pullRequest, addFileToForkedResp := actionsTrustTestCreatePullRequestFromForkedRepo(t, ownerUser, baseRepo, regularUser)
		pullRequestLink := pullRequest.Issue.Link()

		// there is one commit in the pull request and it is blocked from running actions pending approval
		{
			actionRun := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{RepoID: baseRepo.ID, CommitSHA: addFileToForkedResp.Commit.SHA})
			assert.True(t, actionRun.NeedApproval)
			assert.Equal(t, actions_model.StatusWaiting, actionRun.Status)
			actionRunJob := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{RunID: actionRun.ID, RepoID: baseRepo.ID})
			assert.Equal(t, actions_model.StatusBlocked, actionRunJob.Status)
		}

		// another commit is pushed to the head branch of the pull request
		otherFileInForkResp := actionsTrustTestRepoModify(t, regularUser, baseRepo, forkedRepo, "add_file_one.txt")

		// there are two commits
		// - the oldest one switched from Blocked to Cancelled and no longer needs approval
		// - the newest one is Blocked and pending approval
		{
			actionRun := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{RepoID: baseRepo.ID, CommitSHA: addFileToForkedResp.Commit.SHA})
			assert.False(t, actionRun.NeedApproval)
			assert.Equal(t, actions_model.StatusCancelled, actionRun.Status)
			actionRunJob := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{RunID: actionRun.ID, RepoID: baseRepo.ID})
			assert.Equal(t, actions_model.StatusCancelled, actionRunJob.Status)

			otherActionRun := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{RepoID: baseRepo.ID, CommitSHA: otherFileInForkResp.Commit.SHA})
			assert.True(t, otherActionRun.NeedApproval)
			assert.Equal(t, actions_model.StatusWaiting, otherActionRun.Status)
			otherActionRunJob := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{RunID: otherActionRun.ID, RepoID: baseRepo.ID})
			assert.Equal(t, actions_model.StatusBlocked, otherActionRunJob.Status)
		}

		actionsTrustTestAssertTrustPanel(t, ownerSession, pullRequestLink)
		actionsTrustTestClickTrustPanel(t, ownerSession, pullRequestLink, string(actions_service.UserTrustDenied))

		// there are two commits, both are Cancelled and not pending approval
		{
			actionRun := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{RepoID: baseRepo.ID, CommitSHA: addFileToForkedResp.Commit.SHA})
			assert.False(t, actionRun.NeedApproval)
			assert.Equal(t, actions_model.StatusCancelled, actionRun.Status)
			actionRunJob := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{RunID: actionRun.ID, RepoID: baseRepo.ID})
			assert.Equal(t, actions_model.StatusCancelled, actionRunJob.Status)

			otherActionRun := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{RepoID: baseRepo.ID, CommitSHA: otherFileInForkResp.Commit.SHA})
			assert.False(t, otherActionRun.NeedApproval)
			assert.Equal(t, actions_model.StatusCancelled, otherActionRun.Status)
			otherActionRunJob := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{RunID: otherActionRun.ID, RepoID: baseRepo.ID})
			assert.Equal(t, actions_model.StatusCancelled, otherActionRunJob.Status)
		}
	})
}
