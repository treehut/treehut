// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	auth_model "forgejo.org/models/auth"
	"forgejo.org/models/db"
	issues_model "forgejo.org/models/issues"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/git"
	"forgejo.org/modules/setting"
	api "forgejo.org/modules/structs"
	"forgejo.org/modules/test"
	pull_service "forgejo.org/services/pull"
	repo_service "forgejo.org/services/repository"
	files_service "forgejo.org/services/repository/files"
	"forgejo.org/tests/forgery"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAPIPullUpdate(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, giteaURL *url.URL) {
		// Create PR to test
		user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
		org26 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 26})
		pr := createOutdatedPR(t, user, org26)

		// Test GetDiverging
		diffCount, err := pull_service.GetDiverging(git.DefaultContext, pr)
		require.NoError(t, err)
		assert.Equal(t, 1, diffCount.Behind)
		assert.Equal(t, 1, diffCount.Ahead)
		require.NoError(t, pr.LoadBaseRepo(db.DefaultContext))
		require.NoError(t, pr.LoadIssue(db.DefaultContext))

		session := loginUser(t, "user2")
		token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteRepository)
		req := NewRequestf(t, "POST", "/api/v1/repos/%s/%s/pulls/%d/update", pr.BaseRepo.OwnerName, pr.BaseRepo.Name, pr.Issue.Index).
			AddTokenAuth(token)
		session.MakeRequest(t, req, http.StatusOK)

		// Test GetDiverging after update
		diffCount, err = pull_service.GetDiverging(git.DefaultContext, pr)
		require.NoError(t, err)
		assert.Equal(t, 0, diffCount.Behind)
		assert.Equal(t, 2, diffCount.Ahead)
	})
}

func TestAPIPullUpdateByRebase(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, giteaURL *url.URL) {
		// Create PR to test
		user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
		org26 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 26})
		pr := createOutdatedPR(t, user, org26)

		// Test GetDiverging
		diffCount, err := pull_service.GetDiverging(git.DefaultContext, pr)
		require.NoError(t, err)
		assert.Equal(t, 1, diffCount.Behind)
		assert.Equal(t, 1, diffCount.Ahead)
		require.NoError(t, pr.LoadBaseRepo(db.DefaultContext))
		require.NoError(t, pr.LoadIssue(db.DefaultContext))

		session := loginUser(t, "user2")
		token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteRepository)
		req := NewRequestf(t, "POST", "/api/v1/repos/%s/%s/pulls/%d/update?style=rebase", pr.BaseRepo.OwnerName, pr.BaseRepo.Name, pr.Issue.Index).
			AddTokenAuth(token)
		session.MakeRequest(t, req, http.StatusOK)

		// Test GetDiverging after update
		diffCount, err = pull_service.GetDiverging(git.DefaultContext, pr)
		require.NoError(t, err)
		assert.Equal(t, 0, diffCount.Behind)
		assert.Equal(t, 1, diffCount.Ahead)
	})
}

func TestAPIPullUpdateBranchProtection(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, giteaURL *url.URL) {
		user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
		baseRepoOwner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 4})
		org26 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 26})
		pr := createOutdatedPR(t, user, org26, baseRepoOwner)

		// Allow edits from maintainers on the PR
		pr.AllowMaintainerEdit = true
		err := issues_model.UpdateAllowEdits(t.Context(), pr)
		require.NoError(t, err)

		session := loginUser(t, user.LoginName)
		token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteRepository)

		// Set up a branch protection rule on the *head* branch such that it cannot be pushed to, which should block
		// updating the PR.
		pr.LoadBaseRepo(t.Context())
		pr.LoadHeadRepo(t.Context())
		req := NewRequestWithJSON(t, "POST",
			fmt.Sprintf("/api/v1/repos/%s/%s/branch_protections", pr.HeadRepo.OwnerName, pr.HeadRepo.Name),
			&api.BranchProtection{
				BranchName: "*",
				RuleName:   "*",
				EnablePush: true,
			}).AddTokenAuth(token)
		MakeRequest(t, req, http.StatusCreated)

		// `session/token` is from the owner of the head branch, and should be allowed to do the update:
		req = NewRequestf(t, "POST", "/api/v1/repos/%s/%s/pulls/%d/update", pr.BaseRepo.OwnerName, pr.BaseRepo.Name, pr.Issue.Index).
			AddTokenAuth(token)
		session.MakeRequest(t, req, http.StatusOK)

		// Switch over to the base repo owner.  Even though this PR is set to allow edits by maintainers, they shouldn't
		// be allowed to update the PR because the head branch is protected by a branch protection rule.
		session = loginUser(t, baseRepoOwner.LoginName)
		token = getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteRepository)
		req = NewRequestf(t, "POST", "/api/v1/repos/%s/%s/pulls/%d/update", pr.BaseRepo.OwnerName, pr.BaseRepo.Name, pr.Issue.Index).
			AddTokenAuth(token)
		session.MakeRequest(t, req, http.StatusForbidden)
	})
}

func TestAPIPullAllowMaintainerEditRestrictedHead(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, giteaURL *url.URL) {
		realBaseRepo := forgery.CreateRepository(t, nil, &forgery.CreateRepositoryOptions{
			Files: forgery.FilesInit{}, // ensure an initial commit is present
		})

		forkUser := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
		forkRepo, err := repo_service.ForkRepositoryAndUpdates(t.Context(), forkUser, forkUser, repo_service.ForkRepoOptions{
			BaseRepo:    realBaseRepo,
			Name:        "repo-pr-update",
			Description: "desc",
		})
		require.NoError(t, err)
		assert.NotNil(t, forkRepo)

		_, err = files_service.ChangeRepoFiles(git.DefaultContext, forkRepo, forkUser, &files_service.ChangeRepoFilesOptions{
			Files: []*files_service.ChangeRepoFile{
				{
					Operation:     "create",
					TreePath:      "File_B",
					ContentReader: strings.NewReader("File B"),
				},
			},
			Message:   "Add File on PR branch",
			OldBranch: "main",
			NewBranch: "main",
			Author: &files_service.IdentityOptions{
				Name:  forkUser.Name,
				Email: forkUser.Email,
			},
			Committer: &files_service.IdentityOptions{
				Name:  forkUser.Name,
				Email: forkUser.Email,
			},
			Dates: &files_service.CommitDateOptions{
				Author:    time.Now(),
				Committer: time.Now(),
			},
		})
		require.NoError(t, err)

		// Create a pull request that is unexpectedly backwards -- normally we'd request a branch from the fork to be
		// merged into the original base repo. But there's nothing preventing us from creating a pull request for the
		// base to be pulled into the fork:

		pullIssue := &issues_model.Issue{
			RepoID:   forkRepo.ID,
			Title:    "Pull Base into Fork",
			PosterID: forkUser.ID,
			Poster:   forkUser,
			IsPull:   true,
		}
		pullRequest := &issues_model.PullRequest{
			HeadRepoID:          realBaseRepo.ID,
			BaseRepoID:          forkRepo.ID,
			HeadBranch:          "main",
			BaseBranch:          "main",
			HeadRepo:            realBaseRepo,
			BaseRepo:            forkRepo,
			Type:                issues_model.PullRequestGitea,
			AllowMaintainerEdit: true,
		}
		err = pull_service.NewPullRequest(git.DefaultContext, forkRepo, pullIssue, nil, nil, pullRequest, nil)
		require.NoError(t, err)

		// forkUser is the owner of forkRepo, and therefore a maintainer of forkRepo.  `AllowMaintainerEdit` should
		// allow forkUser to edit the head of the PR... except that, in this case, the owner of the PR delegated that
		// edit access but they never had that edit access.  Try to modify the head repo & branch to see.
		session := loginUser(t, forkUser.LoginName)
		token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteRepository)

		// Attempt modification by editing the realBase's main branch via API:
		req := NewRequestWithJSON(t, "POST", fmt.Sprintf("/api/v1/repos/%s/%s/contents/File_A", realBaseRepo.OwnerName, realBaseRepo.Name),
			&api.CreateFileOptions{
				FileOptions: api.FileOptions{
					BranchName:    "main",
					NewBranchName: "main",
					Message:       "illegal change",
					Author: api.Identity{
						Name:  forkUser.FullName,
						Email: forkUser.Email,
					},
					Committer: api.Identity{
						Name:  forkUser.FullName,
						Email: forkUser.Email,
					},
				},
				ContentBase64: base64.StdEncoding.EncodeToString([]byte("Some content.")),
			}).
			AddTokenAuth(token)
		MakeRequest(t, req, http.StatusForbidden)

		// Modify by "updating" the PR, which would pull the base into the head, even though we don't have access to
		// write the head:
		req = NewRequestf(t, "POST", "/api/v1/repos/%s/%s/pulls/%d/update", forkRepo.OwnerName, forkRepo.Name, pullIssue.Index).
			AddTokenAuth(token)
		MakeRequest(t, req, http.StatusInternalServerError) // probably should be Forbidden
	})
}

func TestAPIViewUpdateSettings(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, giteaURL *url.URL) {
		// Create PR to test
		user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
		org26 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 26})
		pr := createOutdatedPR(t, user, org26)

		// Test GetDiverging
		diffCount, err := pull_service.GetDiverging(git.DefaultContext, pr)
		require.NoError(t, err)
		assert.Equal(t, 1, diffCount.Behind)
		assert.Equal(t, 1, diffCount.Ahead)
		require.NoError(t, pr.LoadBaseRepo(db.DefaultContext))
		require.NoError(t, pr.LoadIssue(db.DefaultContext))

		session := loginUser(t, "user2")
		token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeAll)

		defaultUpdateStyle := "rebase"
		editOption := api.EditRepoOption{
			DefaultUpdateStyle: &defaultUpdateStyle,
		}

		req := NewRequestWithJSON(t, "PATCH", fmt.Sprintf("/api/v1/repos/%s/%s", pr.BaseRepo.OwnerName, pr.BaseRepo.Name), editOption).AddTokenAuth(token)
		session.MakeRequest(t, req, http.StatusOK)
		assertViewPullUpdate(t, pr, session, "rebase", true)

		defaultUpdateStyle = "merge"
		req = NewRequestWithJSON(t, "PATCH", fmt.Sprintf("/api/v1/repos/%s/%s", pr.BaseRepo.OwnerName, pr.BaseRepo.Name), editOption).AddTokenAuth(token)
		session.MakeRequest(t, req, http.StatusOK)
		assertViewPullUpdate(t, pr, session, "merge", true)
	})
}

func TestViewPullUpdateByMerge(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, giteaURL *url.URL) {
		testViewPullUpdate(t, "merge")
	})
}

func TestViewPullUpdateByRebase(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, giteaURL *url.URL) {
		testViewPullUpdate(t, "rebase")
	})
}

func testViewPullUpdate(t *testing.T, updateStyle string) {
	defer test.MockVariableValue(&setting.Repository.PullRequest.DefaultUpdateStyle, updateStyle)()
	// Create PR to test
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	org26 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 26})
	pr := createOutdatedPR(t, user, org26)

	// Test GetDiverging
	diffCount, err := pull_service.GetDiverging(git.DefaultContext, pr)
	require.NoError(t, err)
	assert.Equal(t, 1, diffCount.Behind)
	assert.Equal(t, 1, diffCount.Ahead)
	require.NoError(t, pr.LoadBaseRepo(db.DefaultContext))
	require.NoError(t, pr.LoadIssue(db.DefaultContext))

	session := loginUser(t, "user2")
	assertViewPullUpdate(t, pr, session, updateStyle, true)
}

func assertViewPullUpdate(t *testing.T, pr *issues_model.PullRequest, session *TestSession, expectedStyle string, dropdownExpected bool) {
	req := NewRequest(t, "GET", fmt.Sprintf("%s/%s/pulls/%d", pr.BaseRepo.OwnerName, pr.BaseRepo.Name, pr.Issue.Index))
	resp := session.MakeRequest(t, req, http.StatusOK)

	htmlDoc := NewHTMLParser(t, resp.Body)
	// Verify that URL of the update button is shown correctly.
	var mainExpectedURL string
	mergeExpectedURL := fmt.Sprintf("/%s/%s/pulls/%d/update?style=merge", pr.BaseRepo.OwnerName, pr.BaseRepo.Name, pr.Issue.Index)
	rebaseExpectedURL := fmt.Sprintf("/%s/%s/pulls/%d/update?style=rebase", pr.BaseRepo.OwnerName, pr.BaseRepo.Name, pr.Issue.Index)
	if expectedStyle == "rebase" {
		mainExpectedURL = rebaseExpectedURL
		if dropdownExpected {
			htmlDoc.AssertElement(t, fmt.Sprintf(".update-button .dropdown .menu .item[data-do=\"%s\"]:not(.active.selected)", mergeExpectedURL), true)
			htmlDoc.AssertElement(t, fmt.Sprintf(".update-button .dropdown .menu .active.selected.item[data-do=\"%s\"]", rebaseExpectedURL), true)
		}
	} else {
		mainExpectedURL = mergeExpectedURL
		if dropdownExpected {
			htmlDoc.AssertElement(t, fmt.Sprintf(".update-button .dropdown .menu .active.selected.item[data-do=\"%s\"]", mergeExpectedURL), true)
			htmlDoc.AssertElement(t, fmt.Sprintf(".update-button .dropdown .menu .item[data-do=\"%s\"]:not(.active.selected)", rebaseExpectedURL), true)
		}
	}
	if dropdownExpected {
		htmlDoc.AssertElement(t, fmt.Sprintf(".update-button .button[data-do=\"%s\"]", mainExpectedURL), true)
	} else {
		htmlDoc.AssertElement(t, fmt.Sprintf("form[action=\"%s\"]", mainExpectedURL), true)
	}
}

func createOutdatedPR(t *testing.T, actor, forkOrg *user_model.User, baseRepoOwnerOption ...*user_model.User) *issues_model.PullRequest {
	baseRepoOwner := actor
	if len(baseRepoOwnerOption) == 1 {
		baseRepoOwner = baseRepoOwnerOption[0]
	}

	baseRepo := forgery.CreateRepository(t, baseRepoOwner, &forgery.CreateRepositoryOptions{
		Files: forgery.FilesInit{}, // ensure an initial commit is present
	})

	headRepo, err := repo_service.ForkRepositoryAndUpdates(git.DefaultContext, actor, forkOrg, repo_service.ForkRepoOptions{
		BaseRepo:    baseRepo,
		Name:        "repo-pr-update",
		Description: "desc",
	})
	require.NoError(t, err)
	assert.NotEmpty(t, headRepo)

	// create a commit on base Repo
	_, err = files_service.ChangeRepoFiles(git.DefaultContext, baseRepo, baseRepoOwner, &files_service.ChangeRepoFilesOptions{
		Files: []*files_service.ChangeRepoFile{
			{
				Operation:     "create",
				TreePath:      "File_A",
				ContentReader: strings.NewReader("File A"),
			},
		},
		Message:   "Add File A",
		OldBranch: "main",
		NewBranch: "main",
		Author: &files_service.IdentityOptions{
			Name:  actor.Name,
			Email: actor.Email,
		},
		Committer: &files_service.IdentityOptions{
			Name:  actor.Name,
			Email: actor.Email,
		},
		Dates: &files_service.CommitDateOptions{
			Author:    time.Now(),
			Committer: time.Now(),
		},
	})
	require.NoError(t, err)

	// create a commit on head Repo
	_, err = files_service.ChangeRepoFiles(git.DefaultContext, headRepo, actor, &files_service.ChangeRepoFilesOptions{
		Files: []*files_service.ChangeRepoFile{
			{
				Operation:     "create",
				TreePath:      "File_B",
				ContentReader: strings.NewReader("File B"),
			},
		},
		Message:   "Add File on PR branch",
		OldBranch: "main",
		NewBranch: "newBranch",
		Author: &files_service.IdentityOptions{
			Name:  actor.Name,
			Email: actor.Email,
		},
		Committer: &files_service.IdentityOptions{
			Name:  actor.Name,
			Email: actor.Email,
		},
		Dates: &files_service.CommitDateOptions{
			Author:    time.Now(),
			Committer: time.Now(),
		},
	})
	require.NoError(t, err)

	// create Pull
	pullIssue := &issues_model.Issue{
		RepoID:   baseRepo.ID,
		Title:    "Test Pull -to-update-",
		PosterID: actor.ID,
		Poster:   actor,
		IsPull:   true,
	}
	pullRequest := &issues_model.PullRequest{
		HeadRepoID: headRepo.ID,
		BaseRepoID: baseRepo.ID,
		HeadBranch: "newBranch",
		BaseBranch: "main",
		HeadRepo:   headRepo,
		BaseRepo:   baseRepo,
		Type:       issues_model.PullRequestGitea,
	}
	err = pull_service.NewPullRequest(git.DefaultContext, baseRepo, pullIssue, nil, nil, pullRequest, nil)
	require.NoError(t, err)

	issue := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{Title: "Test Pull -to-update-"})
	require.NoError(t, issue.LoadPullRequest(db.DefaultContext))

	return issue.PullRequest
}

func TestStatusDuringUpdate(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		session := loginUser(t, "user2")

		// Adjust this pull request to be in the conflict checker and having a head
		// branch that is pointing to the an incorrect commit ID.
		_, err := db.GetEngine(t.Context()).Cols("status", "head_branch").Update(&issues_model.PullRequest{ID: 5, Status: issues_model.PullRequestStatusChecking, HeadBranch: "master"})
		require.NoError(t, err)

		resp := session.MakeRequest(t, NewRequest(t, "GET", "/user2/repo1/pulls/5"), http.StatusOK)
		htmlDoc := NewHTMLParser(t, resp.Body)

		assert.Contains(t, htmlDoc.Find(".merge-section .item").Text(), "Merge conflict checking is in progress. Try again in few moments.")
	})
}
