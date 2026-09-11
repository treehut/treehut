// Copyright 2019 The Gitea Authors. All rights reserved.
// Copyright 2024 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	auth_model "forgejo.org/models/auth"
	"forgejo.org/models/db"
	issues_model "forgejo.org/models/issues"
	repo_model "forgejo.org/models/repo"
	unit_model "forgejo.org/models/unit"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/git"
	"forgejo.org/modules/gitrepo"
	"forgejo.org/modules/optional"
	repo_module "forgejo.org/modules/repository"
	api "forgejo.org/modules/structs"
	"forgejo.org/modules/test"
	issue_service "forgejo.org/services/issue"
	"forgejo.org/services/mailer"
	pull_service "forgejo.org/services/pull"
	repo_service "forgejo.org/services/repository"
	files_service "forgejo.org/services/repository/files"
	"forgejo.org/tests"
	"forgejo.org/tests/forgery"

	"github.com/PuerkitoBio/goquery"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/html"
)

func TestPullView_ReviewerMissed(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	session := loginUser(t, "user1")

	req := NewRequest(t, "GET", "/pulls")
	resp := session.MakeRequest(t, req, http.StatusOK)
	assert.True(t, test.IsNormalPageCompleted(resp.Body.String()))

	req = NewRequest(t, "GET", "/user2/repo1/pulls/3")
	resp = session.MakeRequest(t, req, http.StatusOK)
	assert.True(t, test.IsNormalPageCompleted(resp.Body.String()))

	// if some reviews are missing, the page shouldn't fail
	reviews, err := issues_model.FindReviews(db.DefaultContext, issues_model.FindReviewOptions{
		IssueID: 2,
	})
	require.NoError(t, err)
	for _, r := range reviews {
		require.NoError(t, issues_model.DeleteReview(db.DefaultContext, r))
	}
	req = NewRequest(t, "GET", "/user2/repo1/pulls/2")
	resp = session.MakeRequest(t, req, http.StatusOK)
	assert.True(t, test.IsNormalPageCompleted(resp.Body.String()))
}

func TestPullRequestParticipants(t *testing.T) {
	defer unittest.OverrideFixtures("tests/integration/fixtures/TestPullRequestParticipants")()
	defer tests.PrepareTestEnv(t)()
	session := loginUser(t, "user1")

	req := NewRequest(t, "GET", "/user2/repo1/pulls/2")
	resp := session.MakeRequest(t, req, http.StatusOK)
	assert.Contains(t, resp.Body.String(), "2 participants")
	assert.Contains(t, resp.Body.String(), `<a href="/user1" data-tooltip-content="user1">`)
	assert.Contains(t, resp.Body.String(), `<a href="/user2" data-tooltip-content="user2">`)
	// does not contain user10 which has a pending review for this issue
	assert.NotContains(t, resp.Body.String(), `<a href="/user10" data-tooltip-content="user10">`)
}

func loadComment(t *testing.T, commentID string) *issues_model.Comment {
	t.Helper()
	id, err := strconv.ParseInt(commentID, 10, 64)
	require.NoError(t, err)
	return unittest.AssertExistsAndLoadBean(t, &issues_model.Comment{ID: id})
}

func TestPullView_SelfReviewNotification(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, giteaURL *url.URL) {
		user1Session := loginUser(t, "user1")
		user2Session := loginUser(t, "user2")

		oldUser1NotificationCount := getUserNotificationCount(t, user1Session)

		oldUser2NotificationCount := getUserNotificationCount(t, user2Session)

		user1 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
		user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})

		repo := forgery.CreateRepository(t, user2, &forgery.CreateRepositoryOptions{
			Files: forgery.MapFS{
				"README.md":  forgery.MapFile("# Hello Reviewer\n"),
				"CODEOWNERS": forgery.MapFile("README.md @user5\n"),
			},
		})

		// we need to add user1 as collaborator so it can be added as reviewer
		err := repo_module.AddCollaborator(db.DefaultContext, repo, user1)
		require.NoError(t, err)

		// create a new branch to prepare for pull request
		err = updateFileInBranch(user2, repo, "README.md", "codeowner-basebranch",
			strings.NewReader("# This is a new project\n"),
		)
		require.NoError(t, err)

		// Create a pull request.
		resp := testPullCreate(t, user2Session, "user2", repo.Name, false, repo.DefaultBranch, "codeowner-basebranch", "Test Pull Request")
		prURL := test.RedirectURL(resp)
		elem := strings.Split(prURL, "/")
		assert.Equal(t, "pulls", elem[3])

		req := NewRequest(t, http.MethodGet, prURL)
		resp = MakeRequest(t, req, http.StatusOK)
		doc := NewHTMLParser(t, resp.Body)
		attributeFilter := fmt.Sprintf("[data-update-url='/%s/%s/issues/request_review']", user2.Name, repo.Name)
		issueID, ok := doc.Find(attributeFilter).Attr("data-issue-id")
		assert.True(t, ok, "doc must contain data-issue-id")

		testAssignReviewer(t, user1Session, user2.Name, repo.Name, issueID, "1", http.StatusOK)

		// both user notification should keep the same notification count since
		// user2 added itself as reviewer.
		notificationCount := getUserNotificationCount(t, user1Session)
		assert.Equal(t, oldUser1NotificationCount, notificationCount)

		notificationCount = getUserNotificationCount(t, user2Session)
		assert.Equal(t, oldUser2NotificationCount, notificationCount)
	})
}

func TestPullView_ResolveInvalidatedReviewComment(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	session := loginUser(t, "user1")

	req := NewRequest(t, "GET", "/user2/repo1/pulls/3/files")
	session.MakeRequest(t, req, http.StatusOK)

	t.Run("single outdated review (line 1)", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		req := NewRequest(t, "GET", "/user2/repo1/pulls/3/files/reviews/new_comment")
		resp := session.MakeRequest(t, req, http.StatusOK)
		doc := NewHTMLParser(t, resp.Body)
		req = NewRequestWithValues(t, "POST", "/user2/repo1/pulls/3/files/reviews/comments", map[string]string{
			"origin":           doc.GetInputValueByName("origin"),
			"latest_commit_id": doc.GetInputValueByName("latest_commit_id"),
			"side":             "proposed",
			"line":             "1",
			"path":             "iso-8859-1.txt",
			"diff_start_cid":   doc.GetInputValueByName("diff_start_cid"),
			"diff_end_cid":     doc.GetInputValueByName("diff_end_cid"),
			"diff_base_cid":    doc.GetInputValueByName("diff_base_cid"),
			"content":          "nitpicking comment",
			"pending_review":   "",
		})
		session.MakeRequest(t, req, http.StatusOK)

		req = NewRequestWithValues(t, "POST", "/user2/repo1/pulls/3/files/reviews/submit", map[string]string{
			"commit_id": doc.GetInputValueByName("latest_commit_id"),
			"content":   "looks good",
			"type":      "comment",
		})
		session.MakeRequest(t, req, http.StatusOK)

		// retrieve comment_id by reloading the comment page
		req = NewRequest(t, "GET", "/user2/repo1/pulls/3")
		resp = session.MakeRequest(t, req, http.StatusOK)
		doc = NewHTMLParser(t, resp.Body)
		commentID, ok := doc.Find(`[data-action="Resolve"]`).Attr("data-comment-id")
		assert.True(t, ok)

		// adjust the database to mark the comment as invalidated
		// (to invalidate it properly, one should push a commit which should trigger this logic,
		// in the meantime, use this quick-and-dirty trick)
		comment := loadComment(t, commentID)
		require.NoError(t, issues_model.UpdateCommentInvalidate(t.Context(), &issues_model.Comment{
			ID:          comment.ID,
			Invalidated: true,
		}))

		req = NewRequestWithValues(t, "POST", "/user2/repo1/issues/resolve_conversation", map[string]string{
			"origin":     "timeline",
			"action":     "Resolve",
			"comment_id": commentID,
		})
		resp = session.MakeRequest(t, req, http.StatusOK)

		// even on template error, the page returns HTTP 200
		// count the comments to ensure success.
		doc = NewHTMLParser(t, resp.Body)
		assert.Len(t, doc.Find(`.comment-code-cloud > .comment`).Nodes, 1)
	})

	t.Run("outdated and newer review (line 2)", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		req := NewRequest(t, "GET", "/user2/repo1/pulls/3/files/reviews/new_comment")
		resp := session.MakeRequest(t, req, http.StatusOK)
		newCommentForm := NewHTMLParser(t, resp.Body)

		var firstReviewID int64
		{
			// first (outdated) review
			req = NewRequestWithValues(t, "POST", "/user2/repo1/pulls/3/files/reviews/comments", map[string]string{
				"origin":           newCommentForm.GetInputValueByName("origin"),
				"latest_commit_id": newCommentForm.GetInputValueByName("latest_commit_id"),
				"side":             "proposed",
				"line":             "2",
				"path":             "iso-8859-1.txt",
				"diff_start_cid":   newCommentForm.GetInputValueByName("diff_start_cid"),
				"diff_end_cid":     newCommentForm.GetInputValueByName("diff_end_cid"),
				"diff_base_cid":    newCommentForm.GetInputValueByName("diff_base_cid"),
				"content":          "nitpicking comment",
				"pending_review":   "",
			})
			session.MakeRequest(t, req, http.StatusOK)

			req = NewRequestWithValues(t, "POST", "/user2/repo1/pulls/3/files/reviews/submit", map[string]string{
				"commit_id": newCommentForm.GetInputValueByName("latest_commit_id"),
				"content":   "looks good",
				"type":      "comment",
			})
			session.MakeRequest(t, req, http.StatusOK)

			// retrieve comment_id by reloading the comment page
			req = NewRequest(t, "GET", "/user2/repo1/pulls/3")
			resp = session.MakeRequest(t, req, http.StatusOK)
			doc := NewHTMLParser(t, resp.Body)
			commentID, ok := doc.Find(`[data-action="Resolve"]`).Attr("data-comment-id")
			assert.True(t, ok)

			// adjust the database to mark the comment as invalidated
			// (to invalidate it properly, one should push a commit which should trigger this logic,
			// in the meantime, use this quick-and-dirty trick)
			comment := loadComment(t, commentID)
			require.NoError(t, issues_model.UpdateCommentInvalidate(t.Context(), &issues_model.Comment{
				ID:          comment.ID,
				Invalidated: true,
			}))
			firstReviewID = comment.ReviewID
			assert.NotZero(t, firstReviewID)
		}

		// ID of the first comment for the second (up-to-date) review
		var commentID string

		{
			// second (up-to-date) review on the same line
			// make a second review
			req = NewRequestWithValues(t, "POST", "/user2/repo1/pulls/3/files/reviews/comments", map[string]string{
				"origin":           newCommentForm.GetInputValueByName("origin"),
				"latest_commit_id": newCommentForm.GetInputValueByName("latest_commit_id"),
				"side":             "proposed",
				"line":             "2",
				"path":             "iso-8859-1.txt",
				"diff_start_cid":   newCommentForm.GetInputValueByName("diff_start_cid"),
				"diff_end_cid":     newCommentForm.GetInputValueByName("diff_end_cid"),
				"diff_base_cid":    newCommentForm.GetInputValueByName("diff_base_cid"),
				"content":          "nitpicking comment",
				"pending_review":   "",
			})
			session.MakeRequest(t, req, http.StatusOK)

			req = NewRequestWithValues(t, "POST", "/user2/repo1/pulls/3/files/reviews/submit", map[string]string{
				"commit_id": newCommentForm.GetInputValueByName("latest_commit_id"),
				"content":   "looks better",
				"type":      "comment",
			})
			session.MakeRequest(t, req, http.StatusOK)

			// retrieve comment_id by reloading the comment page
			req = NewRequest(t, "GET", "/user2/repo1/pulls/3")
			resp = session.MakeRequest(t, req, http.StatusOK)
			doc := NewHTMLParser(t, resp.Body)

			commentIDs := doc.Find(`[data-action="Resolve"]`).Map(func(i int, elt *goquery.Selection) string {
				v, _ := elt.Attr("data-comment-id")
				return v
			})
			assert.Len(t, commentIDs, 2) // 1 for the outdated review, 1 for the current review

			// check that the first comment is for the previous review
			comment := loadComment(t, commentIDs[0])
			assert.Equal(t, comment.ReviewID, firstReviewID)

			// check that the second comment is for a different review
			comment = loadComment(t, commentIDs[1])
			assert.NotZero(t, comment.ReviewID)
			assert.NotEqual(t, comment.ReviewID, firstReviewID)

			commentID = commentIDs[1] // save commentID for later
		}

		req = NewRequestWithValues(t, "POST", "/user2/repo1/issues/resolve_conversation", map[string]string{
			"origin":     "timeline",
			"action":     "Resolve",
			"comment_id": commentID,
		})
		resp = session.MakeRequest(t, req, http.StatusOK)

		// even on template error, the page returns HTTP 200
		// count the comments to ensure success.
		doc := NewHTMLParser(t, resp.Body)
		comments := doc.Find(`.comment-code-cloud > .comment`)
		assert.Len(t, comments.Nodes, 1) // the outdated comment belongs to another review and should not be shown
	})

	t.Run("Files Changed tab", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		for _, c := range []struct {
			style, outdated string
			expectedCount   int
		}{
			{"unified", "true", 3},  // 1 comment on line 1 + 2 comments on line 3
			{"unified", "false", 1}, // 1 comment on line 3 is not outdated
			{"split", "true", 3},    // 1 comment on line 1 + 2 comments on line 3
			{"split", "false", 1},   // 1 comment on line 3 is not outdated
		} {
			t.Run(c.style+"+"+c.outdated, func(t *testing.T) {
				req := NewRequest(t, "GET", "/user2/repo1/pulls/3/files?style="+c.style+"&show-outdated="+c.outdated)
				resp := session.MakeRequest(t, req, http.StatusOK)

				doc := NewHTMLParser(t, resp.Body)
				comments := doc.Find(`.comments > .comment`)
				assert.Len(t, comments.Nodes, c.expectedCount)
			})
		}
	})

	t.Run("Conversation tab", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		req := NewRequest(t, "GET", "/user2/repo1/pulls/3")
		resp := session.MakeRequest(t, req, http.StatusOK)

		doc := NewHTMLParser(t, resp.Body)
		comments := doc.Find(`.comment-code-cloud > .comment`)
		assert.Len(t, comments.Nodes, 3) // 1 comment on line 1 + 2 comments on line 3
	})
}

func TestPullView_CodeOwner(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})

		repo := forgery.CreateRepository(t, user2, &forgery.CreateRepositoryOptions{
			Files: forgery.MapFS{
				"README.md":  forgery.MapFile("# Hello CodeOwner\n"),
				"CODEOWNERS": forgery.MapFile("README.md @user5\n"),
			},
		})

		t.Run("First Pull Request", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			// create a new branch to prepare for pull request
			err := updateFileInBranch(user2, repo, "README.md", "codeowner-basebranch",
				strings.NewReader("# This is a new project\n"),
			)
			require.NoError(t, err)

			// Create a pull request.
			session := loginUser(t, "user2")
			testPullCreate(t, session, "user2", repo.Name, false, repo.DefaultBranch, "codeowner-basebranch", "Test Pull Request")

			pr := unittest.AssertExistsAndLoadBean(t, &issues_model.PullRequest{BaseRepoID: repo.ID, HeadRepoID: repo.ID, HeadBranch: "codeowner-basebranch"})
			unittest.AssertExistsIf(t, true, &issues_model.Review{IssueID: pr.IssueID, Type: issues_model.ReviewTypeRequest, ReviewerID: 5})
			require.NoError(t, pr.LoadIssue(db.DefaultContext))

			err = issue_service.ChangeTitle(db.DefaultContext, pr.Issue, user2, "[WIP] Test Pull Request")
			require.NoError(t, err)
			prUpdated1 := unittest.AssertExistsAndLoadBean(t, &issues_model.PullRequest{ID: pr.ID})
			require.NoError(t, prUpdated1.LoadIssue(db.DefaultContext))
			assert.Equal(t, "[WIP] Test Pull Request", prUpdated1.Issue.Title)

			err = issue_service.ChangeTitle(db.DefaultContext, prUpdated1.Issue, user2, "Test Pull Request2")
			require.NoError(t, err)
			prUpdated2 := unittest.AssertExistsAndLoadBean(t, &issues_model.PullRequest{ID: pr.ID})
			require.NoError(t, prUpdated2.LoadIssue(db.DefaultContext))
			assert.Equal(t, "Test Pull Request2", prUpdated2.Issue.Title)
		})

		// change the default branch CODEOWNERS file to change README.md's codeowner
		err := updateFileInBranch(user2, repo, "CODEOWNERS", "",
			strings.NewReader("README.md @user8\n"),
		)
		require.NoError(t, err)

		t.Run("Second Pull Request", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			// create a new branch to prepare for pull request
			err := updateFileInBranch(user2, repo, "README.md", "codeowner-basebranch2",
				strings.NewReader("# This is a new project2\n"),
			)
			require.NoError(t, err)

			// Create a pull request.
			session := loginUser(t, "user2")
			testPullCreate(t, session, "user2", repo.Name, false, repo.DefaultBranch, "codeowner-basebranch2", "Test Pull Request2")

			pr := unittest.AssertExistsAndLoadBean(t, &issues_model.PullRequest{BaseRepoID: repo.ID, HeadBranch: "codeowner-basebranch2"})
			unittest.AssertExistsIf(t, true, &issues_model.Review{IssueID: pr.IssueID, Type: issues_model.ReviewTypeRequest, ReviewerID: 8})
		})

		t.Run("Forked Repo Pull Request", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			user5 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 5})
			forkedRepo, err := repo_service.ForkRepositoryAndUpdates(db.DefaultContext, user2, user5, repo_service.ForkRepoOptions{
				BaseRepo: repo,
				Name:     "test_codeowner_fork",
			})
			require.NoError(t, err)

			// create a new branch to prepare for pull request
			err = updateFileInBranch(user5, forkedRepo, "README.md", "codeowner-basebranch-forked",
				strings.NewReader("# This is a new forked project\n"),
			)
			require.NoError(t, err)

			session := loginUser(t, "user5")

			// create a pull request on the forked repository, code reviewers should not be mentioned
			testPullCreateDirectly(t, session, "user5", "test_codeowner_fork", forkedRepo.DefaultBranch, "", "", "codeowner-basebranch-forked", "Test Pull Request on Forked Repository")

			pr := unittest.AssertExistsAndLoadBean(t, &issues_model.PullRequest{BaseRepoID: forkedRepo.ID, HeadBranch: "codeowner-basebranch-forked"})
			unittest.AssertExistsIf(t, false, &issues_model.Review{IssueID: pr.IssueID, Type: issues_model.ReviewTypeRequest, ReviewerID: 8})

			// create a pull request to base repository, code reviewers should be mentioned
			testPullCreateDirectly(t, session, repo.OwnerName, repo.Name, repo.DefaultBranch, forkedRepo.OwnerName, forkedRepo.Name, "codeowner-basebranch-forked", "Test Pull Request3")

			pr = unittest.AssertExistsAndLoadBean(t, &issues_model.PullRequest{BaseRepoID: repo.ID, HeadRepoID: forkedRepo.ID, HeadBranch: "codeowner-basebranch-forked"})
			unittest.AssertExistsIf(t, true, &issues_model.Review{IssueID: pr.IssueID, Type: issues_model.ReviewTypeRequest, ReviewerID: 8})
		})
	})
}

func TestPullView_GivenApproveOrRejectReviewOnClosedPR(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, giteaURL *url.URL) {
		user1Session := loginUser(t, "user1")
		user2Session := loginUser(t, "user2")

		// Have user1 create a fork of repo1.
		testRepoFork(t, user1Session, "user2", "repo1", "user1", "repo1")

		baseRepo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{OwnerName: "user2", Name: "repo1"})
		forkedRepo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{OwnerName: "user1", Name: "repo1"})
		baseGitRepo, err := gitrepo.OpenRepository(db.DefaultContext, baseRepo)
		require.NoError(t, err)
		defer baseGitRepo.Close()

		t.Run("Submit approve/reject review on merged PR", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			// Create a merged PR (made by user1) in the upstream repo1.
			testEditFile(t, user1Session, "user1", "repo1", "master", "README.md", "Hello, World (Edited)\n")
			resp := testPullCreate(t, user1Session, "user1", "repo1", false, "master", "master", "This is a pull title")
			elem := strings.Split(test.RedirectURL(resp), "/")
			assert.Equal(t, "pulls", elem[3])
			testPullMerge(t, user1Session, elem[1], elem[2], elem[4], repo_model.MergeStyleMerge, false)

			// Get the commit SHA
			pr := unittest.AssertExistsAndLoadBean(t, &issues_model.PullRequest{
				BaseRepoID: baseRepo.ID,
				BaseBranch: "master",
				HeadRepoID: forkedRepo.ID,
				HeadBranch: "master",
			})
			sha, err := baseGitRepo.GetRefCommitID(pr.GetGitRefName())
			require.NoError(t, err)

			// Submit an approve review on the PR.
			testSubmitReview(t, user2Session, "user2", "repo1", elem[4], sha, "approve", http.StatusOK)

			// Submit a reject review on the PR.
			testSubmitReview(t, user2Session, "user2", "repo1", elem[4], sha, "reject", http.StatusOK)
		})

		t.Run("Submit approve/reject review on closed PR", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			// Created a closed PR (made by user1) in the upstream repo1.
			testEditFileToNewBranch(t, user1Session, "user1", "repo1", "master", "a-test-branch", "README.md", "Hello, World (Edited...again)\n")
			resp := testPullCreate(t, user1Session, "user1", "repo1", false, "master", "a-test-branch", "This is a pull title")
			elem := strings.Split(test.RedirectURL(resp), "/")
			assert.Equal(t, "pulls", elem[3])
			testIssueClose(t, user1Session, elem[1], elem[2], elem[4], true)

			// Get the commit SHA
			pr := unittest.AssertExistsAndLoadBean(t, &issues_model.PullRequest{
				BaseRepoID: baseRepo.ID,
				BaseBranch: "master",
				HeadRepoID: forkedRepo.ID,
				HeadBranch: "a-test-branch",
			})
			sha, err := baseGitRepo.GetRefCommitID(pr.GetGitRefName())
			require.NoError(t, err)

			// Submit an approve review on the PR.
			testSubmitReview(t, user2Session, "user2", "repo1", elem[4], sha, "approve", http.StatusOK)

			// Submit a reject review on the PR.
			testSubmitReview(t, user2Session, "user2", "repo1", elem[4], sha, "reject", http.StatusOK)
		})
	})
}

func TestPullReview_OldLatestCommitId(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	session := loginUser(t, "user1")

	baseRepo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{OwnerName: "user2", Name: "repo1"})
	pr := unittest.AssertExistsAndLoadBean(t, &issues_model.PullRequest{BaseRepoID: baseRepo.ID, Index: 3})

	baseGitRepo, err := gitrepo.OpenRepository(db.DefaultContext, baseRepo)
	require.NoError(t, err)
	defer baseGitRepo.Close()

	headCommitSHA, err := baseGitRepo.GetRefCommitID(pr.GetGitRefName())
	require.NoError(t, err)

	headCommit, err := baseGitRepo.GetCommit(headCommitSHA)
	require.NoError(t, err)
	require.GreaterOrEqual(t, headCommit.ParentCount(), 1)

	parentCommit, err := headCommit.Parent(0)
	require.NoError(t, err)
	oldCommitSHA := parentCommit.ID.String()
	require.NotEqual(t, headCommitSHA, oldCommitSHA)

	req := NewRequest(t, "GET", "/user2/repo1/pulls/3/files/reviews/new_comment")
	resp := session.MakeRequest(t, req, http.StatusOK)
	doc := NewHTMLParser(t, resp.Body)

	const content = "TestPullReview_OldLatestCommitId"
	req = NewRequestWithValues(t, "POST", "/user2/repo1/pulls/3/files/reviews/comments", map[string]string{
		"origin":           doc.GetInputValueByName("origin"),
		"latest_commit_id": oldCommitSHA,
		"side":             "proposed",
		"line":             "2",
		"path":             "iso-8859-1.txt",
		"diff_start_cid":   doc.GetInputValueByName("diff_start_cid"),
		"diff_end_cid":     doc.GetInputValueByName("diff_end_cid"),
		"diff_base_cid":    doc.GetInputValueByName("diff_base_cid"),
		"content":          content,
		"single_review":    "true",
	})
	session.MakeRequest(t, req, http.StatusOK)

	comment := unittest.AssertExistsAndLoadBean(t, &issues_model.Comment{IssueID: pr.IssueID, Content: content})
	require.NotZero(t, comment.ReviewID)
	assert.Equal(t, oldCommitSHA, comment.CommitSHA)
	assert.NotEqual(t, headCommitSHA, comment.CommitSHA)

	review := unittest.AssertExistsAndLoadBean(t, &issues_model.Review{ID: comment.ReviewID})
	assert.Equal(t, issues_model.ReviewTypeComment, review.Type)
	assert.Equal(t, oldCommitSHA, review.CommitID)
	assert.NotEqual(t, headCommitSHA, review.CommitID)
}

// TestPullReviewReplyToCommentWithoutPatch checks that replying to a code comment that has no stored
// patch (e.g. migrated from GitHub against a commit later force-pushed away) does not 500 when the
// diff hunk cannot be regenerated; the reply is stored without diff context instead.
func TestPullReviewReplyToCommentWithoutPatch(t *testing.T) {
	defer unittest.OverrideFixtures("tests/integration/fixtures/TestPullReviewReplyToCommentWithoutPatch")()
	defer tests.PrepareTestEnv(t)()

	session := loginUser(t, "user2")

	review := unittest.AssertExistsAndLoadBean(t, &issues_model.Review{ID: 1100})

	// Warm up the session, then reply with an empty latest_commit_id: this mirrors the timeline
	// reply form, which submits no commit id, so the diff hunk cannot be regenerated.
	session.MakeRequest(t, NewRequest(t, "GET", "/user2/repo1/pulls/2/files/reviews/new_comment"), http.StatusOK)

	const content = "reply to a comment migrated without a patch"
	req := NewRequestWithValues(t, "POST", "/user2/repo1/pulls/2/files/reviews/comments", map[string]string{
		"origin":           "timeline",
		"latest_commit_id": "",
		"content":          content,
		"side":             "proposed",
		"line":             "4",
		"path":             "README.md",
		"reply":            strconv.FormatInt(review.ID, 10),
	})
	session.MakeRequest(t, req, http.StatusOK)

	reply := unittest.AssertExistsAndLoadBean(t, &issues_model.Comment{Content: content, IssueID: 2})
	assert.Equal(t, review.ID, reply.ReviewID)
	assert.Empty(t, reply.Patch)
}

func TestPullReviewInArchivedRepo(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, giteaURL *url.URL) {
		session := loginUser(t, "user2")

		// Open a PR
		testEditFileToNewBranch(t, session, "user2", "repo1", "master", "for-pr", "README.md", "Hi!\n")
		resp := testPullCreate(t, session, "user2", "repo1", true, "master", "for-pr", "PR title")
		elem := strings.Split(test.RedirectURL(resp), "/")

		t.Run("Review box normally", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			// The "Finish review button" must be available
			resp = session.MakeRequest(t, NewRequest(t, "GET", path.Join(elem[1], elem[2], "pulls", elem[4], "files")), http.StatusOK)
			button := NewHTMLParser(t, resp.Body).Find("#review-box button")
			assert.False(t, button.HasClass("disabled"))
		})

		t.Run("Review box in archived repo", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			// Archive the repo
			resp = session.MakeRequest(t, NewRequestWithValues(t, "POST", path.Join(elem[1], elem[2], "settings"), map[string]string{
				"action": "archive",
			}), http.StatusSeeOther)

			// The "Finish review button" must be disabled
			resp = session.MakeRequest(t, NewRequest(t, "GET", path.Join(elem[1], elem[2], "pulls", elem[4], "files")), http.StatusOK)
			button := NewHTMLParser(t, resp.Body).Find("#review-box button")
			assert.True(t, button.HasClass("disabled"))
		})
	})
}

func testNotificationCount(t *testing.T, session *TestSession, expectedSubmitStatus int) *httptest.ResponseRecorder {
	options := map[string]string{}

	req := NewRequestWithValues(t, "GET", "/", options)
	return session.MakeRequest(t, req, expectedSubmitStatus)
}

func testAssignReviewer(t *testing.T, session *TestSession, owner, repo, pullID, reviewer string, expectedSubmitStatus int) *httptest.ResponseRecorder {
	options := map[string]string{
		"action":    "attach",
		"issue_ids": pullID,
		"id":        reviewer,
	}

	submitURL := path.Join(owner, repo, "issues", "request_review")
	req := NewRequestWithValues(t, "POST", submitURL, options)
	return session.MakeRequest(t, req, expectedSubmitStatus)
}

func testSubmitReview(t *testing.T, session *TestSession, owner, repo, pullNumber, commitID, reviewType string, expectedSubmitStatus int) *httptest.ResponseRecorder {
	options := map[string]string{
		"commit_id": commitID,
		"content":   "test",
		"type":      reviewType,
	}

	submitURL := path.Join(owner, repo, "pulls", pullNumber, "files", "reviews", "submit")
	req := NewRequestWithValues(t, "POST", submitURL, options)
	return session.MakeRequest(t, req, expectedSubmitStatus)
}

func testIssueClose(t *testing.T, session *TestSession, owner, repo, issueNumber string, isPull bool) *httptest.ResponseRecorder {
	closeURL := path.Join(owner, repo, "issues", issueNumber, "comments")
	req := NewRequestWithValues(t, "POST", closeURL, map[string]string{
		"status": "close",
	})
	return session.MakeRequest(t, req, http.StatusOK)
}

func getUserNotificationCount(t *testing.T, session *TestSession) string {
	resp := testNotificationCount(t, session, http.StatusOK)
	doc := NewHTMLParser(t, resp.Body)
	return doc.Find(`.notification_count`).Text()
}

func TestPullRequestReplyMail(t *testing.T) {
	defer unittest.OverrideFixtures("tests/integration/fixtures/TestPullRequestReplyMail")()
	defer tests.PrepareTestEnv(t)()

	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	session := loginUser(t, user.Name)

	t.Run("Reply to pending review comment", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		called := false
		defer test.MockVariableValue(&mailer.SendAsync, func(...*mailer.Message) {
			called = true
		})()

		review := unittest.AssertExistsAndLoadBean(t, &issues_model.Review{ID: 1002}, "type = 0")

		req := NewRequest(t, "GET", "/user2/repo1/pulls/2/files/reviews/new_comment")
		resp := session.MakeRequest(t, req, http.StatusOK)
		doc := NewHTMLParser(t, resp.Body)
		req = NewRequestWithValues(t, "POST", "/user2/repo1/pulls/2/files/reviews/comments", map[string]string{
			"origin":           "diff",
			"latest_commit_id": doc.GetInputValueByName("latest_commit_id"),
			"content":          "Just a comment!",
			"side":             "proposed",
			"line":             "4",
			"path":             "README.md",
			"reply":            strconv.FormatInt(review.ID, 10),
		})
		session.MakeRequest(t, req, http.StatusOK)

		assert.False(t, called)
		unittest.AssertExistsIf(t, true, &issues_model.Comment{Content: "Just a comment!", ReviewID: review.ID, IssueID: 2})
	})

	t.Run("Start a review", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		called := false
		defer test.MockVariableValue(&mailer.SendAsync, func(msgs ...*mailer.Message) {
			called = true
		})()

		req := NewRequest(t, "GET", "/user2/repo1/pulls/2/files/reviews/new_comment")
		resp := session.MakeRequest(t, req, http.StatusOK)
		doc := NewHTMLParser(t, resp.Body)
		req = NewRequestWithValues(t, "POST", "/user2/repo1/pulls/2/files/reviews/comments", map[string]string{
			"origin":           "diff",
			"latest_commit_id": doc.GetInputValueByName("latest_commit_id"),
			"content":          "Notification time 2!",
			"side":             "proposed",
			"line":             "2",
			"path":             "README.md",
		})
		session.MakeRequest(t, req, http.StatusOK)

		assert.False(t, called)
		unittest.AssertExistsIf(t, true, &issues_model.Comment{Content: "Notification time 2!", IssueID: 2})
	})

	t.Run("Create a single comment", func(t *testing.T) {
		t.Run("As a reply", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			called := false
			defer test.MockVariableValue(&mailer.SendAsync, func(msgs ...*mailer.Message) {
				assert.Len(t, msgs, 2)
				SortMailerMessages(msgs)
				assert.Equal(t, "user1@example.com", msgs[0].To)
				assert.Equal(t, "Re: [user2/repo1] issue2 (PR #2)", msgs[0].Subject)
				assert.Contains(t, msgs[0].Body, "Notification time!")
				called = true
			})()

			review := unittest.AssertExistsAndLoadBean(t, &issues_model.Review{ID: 1001, Type: issues_model.ReviewTypeComment})

			req := NewRequest(t, "GET", "/user2/repo1/pulls/2/files/reviews/new_comment")
			resp := session.MakeRequest(t, req, http.StatusOK)
			doc := NewHTMLParser(t, resp.Body)
			req = NewRequestWithValues(t, "POST", "/user2/repo1/pulls/2/files/reviews/comments", map[string]string{
				"origin":           "diff",
				"latest_commit_id": doc.GetInputValueByName("latest_commit_id"),
				"content":          "Notification time!",
				"side":             "proposed",
				"line":             "3",
				"path":             "README.md",
				"reply":            strconv.FormatInt(review.ID, 10),
			})
			session.MakeRequest(t, req, http.StatusOK)

			assert.True(t, called)
			unittest.AssertExistsIf(t, true, &issues_model.Comment{Content: "Notification time!", ReviewID: review.ID, IssueID: 2})
		})
		t.Run("On a new line", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			called := false
			defer test.MockVariableValue(&mailer.SendAsync, func(msgs ...*mailer.Message) {
				assert.Len(t, msgs, 2)
				SortMailerMessages(msgs)
				assert.Equal(t, "user1@example.com", msgs[0].To)
				assert.Equal(t, "Re: [user2/repo1] issue2 (PR #2)", msgs[0].Subject)
				assert.Contains(t, msgs[0].Body, "Notification time 2!")
				called = true
			})()

			req := NewRequest(t, "GET", "/user2/repo1/pulls/2/files/reviews/new_comment")
			resp := session.MakeRequest(t, req, http.StatusOK)
			doc := NewHTMLParser(t, resp.Body)
			req = NewRequestWithValues(t, "POST", "/user2/repo1/pulls/2/files/reviews/comments", map[string]string{
				"origin":           "diff",
				"latest_commit_id": doc.GetInputValueByName("latest_commit_id"),
				"content":          "Notification time 2!",
				"side":             "proposed",
				"line":             "5",
				"path":             "README.md",
				"single_review":    "true",
			})
			session.MakeRequest(t, req, http.StatusOK)

			assert.True(t, called)
			unittest.AssertExistsIf(t, true, &issues_model.Comment{Content: "Notification time 2!", IssueID: 2})
		})
	})
}

func updateFileInBranch(user *user_model.User, repo *repo_model.Repository, treePath, newBranch string, content io.ReadSeeker) error {
	oldBranch, err := gitrepo.GetDefaultBranch(git.DefaultContext, repo)
	if err != nil {
		return err
	}

	commitID, err := gitrepo.GetBranchCommitID(git.DefaultContext, repo, oldBranch)
	if err != nil {
		return err
	}

	opts := &files_service.ChangeRepoFilesOptions{
		Files: []*files_service.ChangeRepoFile{
			{
				Operation:     "update",
				TreePath:      treePath,
				ContentReader: content,
			},
		},
		OldBranch:    oldBranch,
		NewBranch:    newBranch,
		Author:       nil,
		Committer:    nil,
		LastCommitID: commitID,
	}
	_, err = files_service.ChangeRepoFiles(git.DefaultContext, repo, user, opts)
	return err
}

func TestPullRequestStaleReview(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
		session := loginUser(t, user2.Name)

		// Create temporary repository.
		repo := forgery.CreateRepository(t, user2, &forgery.CreateRepositoryOptions{
			Files: forgery.MapFS{
				"FUNFACT": forgery.MapFile("Smithy was the runner up to be Forgejo's name"),
			},
		})
		forgery.EnableRepoUnits(t, repo, unit_model.TypePullRequests)

		clone := func(t *testing.T, clone string) string {
			t.Helper()

			dstPath := t.TempDir()
			cloneURL, _ := url.Parse(clone)
			cloneURL.User = url.UserPassword("user2", userPassword)
			require.NoError(t, git.CloneWithArgs(t.Context(), nil, cloneURL.String(), dstPath, git.CloneRepoOptions{}))
			doGitSetRemoteURL(dstPath, "origin", cloneURL)(t)

			return dstPath
		}

		firstCommit := func(t *testing.T, dstPath string) string {
			t.Helper()

			require.NoError(t, os.WriteFile(path.Join(dstPath, "README.md"), []byte("## test content"), 0o600))
			require.NoError(t, git.AddChanges(dstPath, true))
			require.NoError(t, git.CommitChanges(dstPath, git.CommitChangesOptions{
				Committer: &git.Signature{
					Email: "user2@example.com",
					Name:  "user2",
					When:  time.Now(),
				},
				Author: &git.Signature{
					Email: "user2@example.com",
					Name:  "user2",
					When:  time.Now(),
				},
				Message: "Add README.",
			}))
			stdout := &bytes.Buffer{}
			require.NoError(t, git.NewCommand(t.Context(), "rev-parse", "HEAD").Run(&git.RunOpts{Dir: dstPath, Stdout: stdout}))

			return strings.TrimSpace(stdout.String())
		}

		secondCommit := func(t *testing.T, dstPath string) {
			require.NoError(t, os.WriteFile(path.Join(dstPath, "README.md"), []byte("## I prefer this heading"), 0o600))
			require.NoError(t, git.AddChanges(dstPath, true))
			require.NoError(t, git.CommitChanges(dstPath, git.CommitChangesOptions{
				Committer: &git.Signature{
					Email: "user2@example.com",
					Name:  "user2",
					When:  time.Now(),
				},
				Author: &git.Signature{
					Email: "user2@example.com",
					Name:  "user2",
					When:  time.Now(),
				},
				Message: "Add README.",
			}))
		}

		firstReview := func(t *testing.T, firstCommitID string, index int64) {
			t.Helper()

			resp := session.MakeRequest(t, NewRequest(t, "GET", fmt.Sprintf("/%s/pulls/%d/files/reviews/new_comment", repo.FullName(), index)), http.StatusOK)
			doc := NewHTMLParser(t, resp.Body)

			req := NewRequestWithValues(t, "POST", fmt.Sprintf("/%s/pulls/%d/files/reviews/comments", repo.FullName(), index), map[string]string{
				"origin":           doc.GetInputValueByName("origin"),
				"latest_commit_id": firstCommitID,
				"side":             "proposed",
				"line":             "1",
				"path":             "FUNFACT",
				"diff_start_cid":   doc.GetInputValueByName("diff_start_cid"),
				"diff_end_cid":     doc.GetInputValueByName("diff_end_cid"),
				"diff_base_cid":    doc.GetInputValueByName("diff_base_cid"),
				"content":          "nitpicking comment",
				"pending_review":   "",
			})
			session.MakeRequest(t, req, http.StatusOK)

			req = NewRequestWithValues(t, "POST", "/"+repo.FullName()+"/pulls/1/files/reviews/submit", map[string]string{
				"commit_id": firstCommitID,
				"content":   "looks good",
				"type":      "comment",
			})
			session.MakeRequest(t, req, http.StatusOK)
		}

		staleReview := func(t *testing.T, firstCommitID string, index int64) {
			// Review based on the first commit, which is a stale review because the
			// PR's head is at the seconnd commit.
			req := NewRequestWithValues(t, "POST", fmt.Sprintf("/%s/pulls/%d/files/reviews/submit", repo.FullName(), index), map[string]string{
				"commit_id": firstCommitID,
				"content":   "looks good",
				"type":      "approve",
			})
			session.MakeRequest(t, req, http.StatusOK)
		}

		t.Run("Across repositories", func(t *testing.T) {
			testRepoFork(t, session, "user2", repo.Name, "org3", "forked-repo")

			// Clone it.
			dstPath := clone(t, fmt.Sprintf("%sorg3/forked-repo.git", u.String()))

			// Create first commit.
			firstCommitID := firstCommit(t, dstPath)

			// Create PR across repositories.
			require.NoError(t, git.NewCommand(t.Context(), "push", "origin", "main").Run(&git.RunOpts{Dir: dstPath}))
			session.MakeRequest(t, NewRequestWithValues(t, "POST", repo.FullName()+"/compare/main...org3/forked-repo:main", map[string]string{
				"title": "pull request",
			}), http.StatusOK)
			pr := unittest.AssertExistsAndLoadBean(t, &issues_model.PullRequest{Index: 1, BaseRepoID: repo.ID})

			t.Run("Mark review as stale", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()

				// Create first review
				firstReview(t, firstCommitID, pr.Index)

				// Review is not stale.
				review := unittest.AssertExistsAndLoadBean(t, &issues_model.Review{IssueID: pr.IssueID})
				assert.False(t, review.Stale)

				// Create second commit
				secondCommit(t, dstPath)

				// Push to PR.
				require.NoError(t, git.NewCommand(t.Context(), "push", "origin", "main").Run(&git.RunOpts{Dir: dstPath}))

				// Review is stale.
				assert.Eventually(t, func() bool {
					return unittest.AssertExistsAndLoadBean(t, &issues_model.Review{IssueID: pr.IssueID}).Stale == true
				}, time.Second*10, time.Microsecond*100)
			})

			t.Run("Create stale review", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()

				// Review based on the first commit, which is a stale review because the
				// PR's head is at the seconnd commit.
				staleReview(t, firstCommitID, pr.Index)

				// There does not exist a review that is not stale, because all reviews
				// are based on the first commit and the PR's head is at the second commit.
				unittest.AssertExistsIf(t, false, &issues_model.Review{IssueID: pr.IssueID}, "stale = false")
			})

			t.Run("Mark unstale", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()

				// Force push the PR to the first commit.
				require.NoError(t, git.NewCommand(t.Context(), "reset", "--hard", "HEAD~1").Run(&git.RunOpts{Dir: dstPath}))
				require.NoError(t, git.NewCommand(t.Context(), "push", "--force-with-lease", "origin", "main").Run(&git.RunOpts{Dir: dstPath}))

				// There does not exist a review that is stale, because all reviews
				// are based on the first commit and thus all reviews are no longer marked
				// as stale.
				assert.Eventually(t, func() bool {
					return !unittest.BeanExists(t, &issues_model.Review{IssueID: pr.IssueID}, "stale = true")
				}, time.Second*10, time.Microsecond*100)
			})

			t.Run("Diff did not change", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()

				// Create a empty commit and push it to the PR.
				require.NoError(t, git.NewCommand(t.Context(), "commit", "--allow-empty", "-m", "Empty commit").Run(&git.RunOpts{Dir: dstPath}))
				require.NoError(t, git.NewCommand(t.Context(), "push", "origin", "main").Run(&git.RunOpts{Dir: dstPath}))

				// There does not exist a review that is stale, because the diff did not
				// change.
				unittest.AssertExistsIf(t, false, &issues_model.Review{IssueID: pr.IssueID}, "stale = true")
			})
		})

		t.Run("AGit", func(t *testing.T) {
			dstPath := clone(t, fmt.Sprintf("%suser2/%s.git", u.String(), repo.Name))

			// Create first commit.
			firstCommitID := firstCommit(t, dstPath)

			// Create agit PR.
			require.NoError(t, git.NewCommand(t.Context(), "push", "origin", "HEAD:refs/for/main", "-o", "topic=agit-pr").Run(&git.RunOpts{Dir: dstPath}))

			pr := unittest.AssertExistsAndLoadBean(t, &issues_model.PullRequest{Index: 2, BaseRepoID: repo.ID})

			t.Run("Mark review as stale", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()

				firstReview(t, firstCommitID, pr.Index)

				// Review is not stale.
				review := unittest.AssertExistsAndLoadBean(t, &issues_model.Review{IssueID: pr.IssueID})
				assert.False(t, review.Stale)

				// Create second commit
				secondCommit(t, dstPath)

				// Push to agit PR.
				require.NoError(t, git.NewCommand(t.Context(), "push", "origin", "HEAD:refs/for/main", "-o", "topic=agit-pr").Run(&git.RunOpts{Dir: dstPath}))

				// Review is stale.
				review = unittest.AssertExistsAndLoadBean(t, &issues_model.Review{IssueID: pr.IssueID})
				assert.True(t, review.Stale)
			})

			t.Run("Create stale review", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()

				// Review based on the first commit, which is a stale review because the
				// PR's head is at the seconnd commit.
				staleReview(t, firstCommitID, pr.Index)

				// There does not exist a review that is not stale, because all reviews
				// are based on the first commit and the PR's head is at the second commit.
				unittest.AssertExistsIf(t, false, &issues_model.Review{IssueID: pr.IssueID}, "stale = false")
			})

			t.Run("Mark unstale", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()

				// Force push the PR to the first commit.
				require.NoError(t, git.NewCommand(t.Context(), "reset", "--hard", "HEAD~1").Run(&git.RunOpts{Dir: dstPath}))
				require.NoError(t, git.NewCommand(t.Context(), "push", "origin", "HEAD:refs/for/main", "-o", "topic=agit-pr", "-o", "force-push").Run(&git.RunOpts{Dir: dstPath}))

				// There does not exist a review that is stale, because all reviews
				// are based on the first commit and thus all reviews are no longer marked
				// as stale.
				unittest.AssertExistsIf(t, false, &issues_model.Review{IssueID: pr.IssueID}, "stale = true")
			})

			t.Run("Diff did not change", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()

				// Create a empty commit and push it to the PR.
				require.NoError(t, git.NewCommand(t.Context(), "commit", "--allow-empty", "-m", "Empty commit").Run(&git.RunOpts{Dir: dstPath}))
				require.NoError(t, git.NewCommand(t.Context(), "push", "origin", "HEAD:refs/for/main", "-o", "topic=agit-pr").Run(&git.RunOpts{Dir: dstPath}))

				// There does not exist a review that is stale, because the diff did not
				// change.
				unittest.AssertExistsIf(t, false, &issues_model.Review{IssueID: pr.IssueID}, "stale = true")
			})
		})
	})
}

func TestPullRequestCommentPlacement(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		t.Run("comment directly on change in PR", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()
			tester := newPullRequestCommentPlacementTester(t)

			commitSHA := tester.changeFile("file1.md",
				strings.Replace(tester.fileContent, "Line 50\n", "Line 50--modified\n", 1))
			tester.createPR()

			comment := tester.commentFromFilesChanged("file1.md", 50)
			assert.Equal(t, `diff --git a/file1.md b/file1.md
--- a/file1.md
+++ b/file1.md
@@ -48,3 +48,3 @@
 Line 48
 Line 49
-Line 50
+Line 50--modified`, comment.PatchQuoted)
			assert.Equal(t, "proposed", comment.DiffSide())
			assert.EqualValues(t, 50, comment.Line)
			assert.Equal(t, commitSHA, comment.CommitSHA)

			diff := []diffTableRow{
				{rowType: RowHasCode, code: "Line 49"},
				{rowType: RowDelCode, code: "Line 50"},
				{rowType: RowAddCode, code: "Line 50--modified"},
				{rowType: RowComment, commentID: comment.ID},
				{rowType: RowHasCode, code: "Line 51"},
			}
			tester.assertFilesChangedDiff(commitSHA, diff)
			tester.assertCommitDiff(commitSHA, diff)
		})

		t.Run("comment lands on blame from commit within PR", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()
			tester := newPullRequestCommentPlacementTester(t)

			// Modify an earlier part of the file in one commit, and a later part of the file in a second commit.
			content := tester.fileContent
			content = strings.Replace(content, "Line 25\n", "Line 25--modified\n", 1)
			commit1 := tester.changeFile("file1.md", content)
			content = strings.Replace(content, "Line 75\n", "Line 75--modified\n", 1)
			commit2 := tester.changeFile("file1.md", content)
			tester.createPR()

			// Comment on the earlier change, from the "Files changed" view; this should "git blame" and be asociated
			// with the first commit where that change was made, therefore appearing on the commit-specific diff later:
			comment := tester.commentFromFilesChanged("file1.md", 25)
			assert.Equal(t, `diff --git a/file1.md b/file1.md
--- a/file1.md
+++ b/file1.md
@@ -23,3 +23,3 @@
 Line 23
 Line 24
-Line 25
+Line 25--modified`, comment.PatchQuoted)
			assert.Equal(t, "proposed", comment.DiffSide())
			assert.EqualValues(t, 25, comment.Line)
			assert.Equal(t, commit1, comment.CommitSHA)

			diff25 := []diffTableRow{
				{rowType: RowHasCode, code: "Line 24"},
				{rowType: RowDelCode, code: "Line 25"},
				{rowType: RowAddCode, code: "Line 25--modified"},
				{rowType: RowComment, commentID: comment.ID},
				{rowType: RowHasCode, code: "Line 26"},
			}
			tester.assertFilesChangedDiff(commit2, diff25)
			tester.assertCommitDiff(commit1, diff25)

			diff75 := []diffTableRow{
				{rowType: RowHasCode, code: "Line 74"},
				{rowType: RowDelCode, code: "Line 75"},
				{rowType: RowAddCode, code: "Line 75--modified"},
				{rowType: RowHasCode, code: "Line 76"},
			}
			tester.assertFilesChangedDiff(commit2, diff75)
			tester.assertCommitDiff(commit2, diff75)
		})

		t.Run("comment lands on blame commit from before PR", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()
			tester := newPullRequestCommentPlacementTester(t)

			// Modify line 50...
			commitSHA := tester.changeFile("file1.md",
				strings.Replace(tester.fileContent, "Line 50\n", "Line 50--modified\n", 1))
			tester.createPR()

			// But while viewing line 50's diff, place a comment on line 49.  This will "git blame" to a commit outside
			// of this PR, but that's fine...
			comment := tester.commentFromFilesChanged("file1.md", 49)
			assert.Equal(t, `diff --git a/file1.md b/file1.md
--- a/file1.md
+++ b/file1.md
@@ -47,7 +47,7 @@ Line 46
 Line 47
 Line 48
 Line 49`, comment.PatchQuoted)
			assert.Equal(t, "proposed", comment.DiffSide())
			assert.EqualValues(t, 49, comment.Line)
			assert.NotEqual(t, commitSHA, comment.CommitSHA)

			diff := []diffTableRow{
				{rowType: RowHasCode, code: "Line 48"},
				{rowType: RowHasCode, code: "Line 49"},
				{rowType: RowComment, commentID: comment.ID},
				{rowType: RowDelCode, code: "Line 50"},
				{rowType: RowAddCode, code: "Line 50--modified"},
				{rowType: RowHasCode, code: "Line 51"},
			}
			tester.assertFilesChangedDiff(commitSHA, diff)
			tester.assertCommitDiff(commitSHA, diff)
		})

		t.Run("comment on line moves due to a following commit", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()
			tester := newPullRequestCommentPlacementTester(t)

			// Modify line 50
			content := tester.fileContent
			content = strings.Replace(content, "Line 50\n", "Line 50--modified\n", 1)
			commit1 := tester.changeFile("file1.md", content)
			tester.createPR()

			// Place a comment on "Line 50--modified"
			comment := tester.commentFromFilesChanged("file1.md", 50)
			assert.Equal(t, `diff --git a/file1.md b/file1.md
--- a/file1.md
+++ b/file1.md
@@ -48,3 +48,3 @@
 Line 48
 Line 49
-Line 50
+Line 50--modified`, comment.PatchQuoted)
			assert.Equal(t, "proposed", comment.DiffSide())
			assert.EqualValues(t, 50, comment.Line)
			assert.Equal(t, commit1, comment.CommitSHA)

			// Add a second commit to the PR which removes  "Line 1" - "Line 10".
			content = strings.Replace(content, "Line 1\nLine 2\nLine 3\nLine 4\nLine 5\nLine 6\nLine 7\nLine 8\nLine 9\nLine 10\n", "", 1)
			commit2 := tester.changeFile("file1.md", content)

			diff2 := []diffTableRow{
				{rowType: RowDelCode, code: "Line 9"},
				{rowType: RowDelCode, code: "Line 10"},
				{rowType: RowHasCode, code: "Line 11"},
			}
			tester.assertFilesChangedDiff(commit2, diff2, "checking commit2 contents in full PR diff")
			tester.assertCommitDiff(commit2, diff2, "checking commit2 contents in single-commit diff")

			diff1 := []diffTableRow{
				{rowType: RowHasCode, code: "Line 49"},
				{rowType: RowDelCode, code: "Line 50"},
				{rowType: RowAddCode, code: "Line 50--modified"},
				{rowType: RowComment, commentID: comment.ID},
				{rowType: RowHasCode, code: "Line 51"},
			}
			tester.assertFilesChangedDiff(commit2, diff1, "checking commit1 contents in full PR diff")
			tester.assertCommitDiff(commit1, diff1, "checking commit1 contents in single-commit diff")
		})

		t.Run("comment on line moves due to a following commit, following commit is rewritten and force-push'd", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()
			tester := newPullRequestCommentPlacementTester(t)

			// Modify line 50
			content := tester.fileContent
			content = strings.Replace(content, "Line 50\n", "Line 50--modified\n", 1)
			commit1 := tester.changeFile("file1.md", content)
			tester.createPR()

			// Place a comment on "Line 50--modified"
			comment := tester.commentFromFilesChanged("file1.md", 50)
			assert.Equal(t, `diff --git a/file1.md b/file1.md
--- a/file1.md
+++ b/file1.md
@@ -48,3 +48,3 @@
 Line 48
 Line 49
-Line 50
+Line 50--modified`, comment.PatchQuoted)
			assert.Equal(t, "proposed", comment.DiffSide())
			assert.EqualValues(t, 50, comment.Line)
			assert.Equal(t, commit1, comment.CommitSHA)
			assert.False(t, comment.Invalidated)

			// Add a second commit to the PR which removes  "Line 1" - "Line 10".
			content = strings.Replace(content, "Line 1\nLine 2\nLine 3\nLine 4\nLine 5\nLine 6\nLine 7\nLine 8\nLine 9\nLine 10\n", "", 1)
			tester.changeFile("file1.md", content)

			// Now amend commit2v1 with an additional change, causing a force push of the branch
			forcePushCommit := tester.withBranchCheckout(func(repoPath string) {
				content = strings.Replace(content, "Line 11\n", "", 1) // Remove Line 11 as well
				require.NoError(t, os.WriteFile(path.Join(repoPath, "file1.md"), []byte(content), 0o644))
				require.NoError(t, git.NewCommand(t.Context(), "commit", "-a", "--amend", "--no-edit").Run(&git.RunOpts{Dir: repoPath}))
				require.NoError(t, git.NewCommand(t.Context(), "push", "--force").Run(&git.RunOpts{Dir: repoPath}))
			})

			diff2 := []diffTableRow{
				{rowType: RowDelCode, code: "Line 10"},
				{rowType: RowDelCode, code: "Line 11"},
				{rowType: RowHasCode, code: "Line 12"},
			}
			tester.assertFilesChangedDiff(forcePushCommit, diff2, "checking commit2 (force push) contents in full PR diff")

			diff1 := []diffTableRow{
				{rowType: RowHasCode, code: "Line 49"},
				{rowType: RowDelCode, code: "Line 50"},
				{rowType: RowAddCode, code: "Line 50--modified"},
				{rowType: RowComment, commentID: comment.ID},
				{rowType: RowHasCode, code: "Line 51"},
			}
			tester.assertFilesChangedDiff(forcePushCommit, diff1, "checking commit1 contents in full PR diff")
			tester.assertCommitDiff(commit1, diff1, "checking commit1 contents in single-commit diff")

			// This comment can still be located in the diff, so it should not be marked as Invalidated/Outdated --
			// which is kinda guaranteed by it being loaded in the diff, but for test sanity assert specifically.
			commentReloaded := unittest.AssertExistsAndLoadBean(t, &issues_model.Comment{ID: comment.ID})
			assert.False(t, commentReloaded.Invalidated)
		})

		t.Run("comment on line commit is rewritten and force-push'd", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()
			tester := newPullRequestCommentPlacementTester(t)

			// Modify line 50
			content := tester.fileContent
			content = strings.Replace(content, "Line 50\n", "Line 50--modified\n", 1)
			commit1 := tester.changeFile("file1.md", content)
			tester.createPR()

			// Place a comment on "Line 50--modified"
			comment := tester.commentFromFilesChanged("file1.md", 50)
			assert.Equal(t, `diff --git a/file1.md b/file1.md
--- a/file1.md
+++ b/file1.md
@@ -48,3 +48,3 @@
 Line 48
 Line 49
-Line 50
+Line 50--modified`, comment.PatchQuoted)
			assert.Equal(t, "proposed", comment.DiffSide())
			assert.EqualValues(t, 50, comment.Line)
			assert.Equal(t, commit1, comment.CommitSHA)
			assert.False(t, comment.Invalidated)

			// Add a second commit to the PR which removes  "Line 1" - "Line 10".
			content = strings.Replace(content, "Line 1\nLine 2\nLine 3\nLine 4\nLine 5\nLine 6\nLine 7\nLine 8\nLine 9\nLine 10\n", "", 1)
			commit2 := tester.changeFile("file1.md", content)

			// Now, reorganize these commits, so that it's main->commit2->commit1 on the branch, rather than
			// main->commit1->commit2. Then force push the branch.
			forcePushCommit := tester.withBranchCheckout(func(repoPath string) {
				// move commit2 onto main, off commit1
				require.NoError(t,
					git.NewCommand(t.Context(), "rebase").
						AddArguments("--onto").AddDynamicArguments(tester.initialSHA).
						AddDynamicArguments(commit1).
						AddDynamicArguments(commit2).
						Run(&git.RunOpts{Dir: repoPath}))
				// move commit1 onto HEAD, off main
				require.NoError(t,
					git.NewCommand(t.Context(), "rebase").
						AddArguments("--onto").AddDynamicArguments("HEAD").
						AddDynamicArguments(tester.initialSHA).
						AddDynamicArguments(commit1).
						Run(&git.RunOpts{Dir: repoPath}))

				// delete branch for the PR
				has, branch := tester.branch.Get()
				require.True(t, has)
				require.NoError(t,
					git.NewCommand(t.Context(), "branch").
						AddArguments("-D").AddDynamicArguments(branch).
						Run(&git.RunOpts{Dir: repoPath}))
				// call HEAD as the branch
				require.NoError(t,
					git.NewCommand(t.Context(), "branch").
						AddDynamicArguments(branch).
						Run(&git.RunOpts{Dir: repoPath}))

				// force push the rebuilt branch
				require.NoError(t, git.NewCommand(t.Context(), "push", "--force", "origin").AddDynamicArguments(branch).Run(&git.RunOpts{Dir: repoPath}))
			})

			diff2 := []diffTableRow{
				{rowType: RowDelCode, code: "Line 10"},
				{rowType: RowHasCode, code: "Line 11"},
			}
			tester.assertFilesChangedDiff(forcePushCommit, diff2, "checking commit2 (force push) contents in full PR diff")

			diff1 := []diffTableRow{
				{rowType: RowHasCode, code: "Line 49"},
				{rowType: RowDelCode, code: "Line 50"},
				{rowType: RowAddCode, code: "Line 50--modified"},
				// no comment visible anymore; force push has lost its place at this time
				{rowType: RowHasCode, code: "Line 51"},
			}
			tester.assertFilesChangedDiff(forcePushCommit, diff1, "checking commit1 contents in full PR diff")

			// After the force push, the comment we originally left should be marked as invalidated since it can no
			// longer be resolved to a code location in the PR head. The above tests validate that it no longer appears
			// in the diff, but this will also happen because of the diff-side check for the correct location -- so
			// let's check that it's invalidated as well, indicating that it will be shown in the UI as "Outdated". This
			// usually passes on the first check but is wrapped in Eventually because the async goroutine used in the
			// pull request testing when the branch is pushed may not be immediately complete.
			assert.EventuallyWithT(t, func(t *assert.CollectT) {
				commentReloaded := unittest.AssertExistsAndLoadBean(t, &issues_model.Comment{ID: comment.ID})
				assert.True(t, commentReloaded.Invalidated)
			}, 1*time.Second, 50*time.Millisecond)
		})

		t.Run("comment lands on blame with original line number varying from current", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()
			tester := newPullRequestCommentPlacementTester(t)

			// Remove "Line 1" - "Line 10", on the base branch. If you "git blame" Line 50 at that point, it will have
			// an original line number 50, but actually be appearing at line number index 40, causing wrong outputs.
			content := tester.fileContent
			content = strings.Replace(content, "Line 1\nLine 2\nLine 3\nLine 4\nLine 5\nLine 6\nLine 7\nLine 8\nLine 9\nLine 10\n", "", 1)
			tester.changeFileOnBase("file1.md", content)

			// Now modify "Line 51" in a PR:
			commitSHA := tester.changeFile("file1.md", strings.Replace(content, "Line 51\n", "Line 51--modified\n", 1))
			tester.createPR()

			// Place a comment on "Line 50", which would "git blame" to the original commit and line number 50, even
			// though it's now actually at line number 40.
			comment := tester.commentFromFilesChanged("file1.md", lineNumber(content, "Line 50"))
			assert.Equal(t, `diff --git a/file1.md b/file1.md
--- a/file1.md
+++ b/file1.md
@@ -38,7 +38,7 @@ Line 47
 Line 48
 Line 49
 Line 50`, comment.PatchQuoted)
			assert.Equal(t, "proposed", comment.DiffSide())
			assert.EqualValues(t, 50, comment.Line)
			assert.Equal(t, tester.initialSHA, comment.CommitSHA)

			diff := []diffTableRow{
				{rowType: RowHasCode, code: "Line 49"},
				{rowType: RowHasCode, code: "Line 50"},
				{rowType: RowComment, commentID: comment.ID},
				{rowType: RowDelCode, code: "Line 51"},
				{rowType: RowAddCode, code: "Line 51--modified"},
				{rowType: RowHasCode, code: "Line 52"},
			}
			tester.assertFilesChangedDiff(commitSHA, diff)
			tester.assertCommitDiff(commitSHA, diff)
		})

		t.Run("comment on specific commit adjusts correctly to later changes in the PR", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()
			tester := newPullRequestCommentPlacementTester(t)

			// Modify an earlier part of the file in one commit, and then change line numbers in a second commit by
			// removing some content from the file earlier than the first commit
			content := tester.fileContent
			content = strings.Replace(content, "Line 50\n", "Line 50--modified\n", 1)
			commit1 := tester.changeFile("file1.md", content)
			content = strings.Replace(content, "Line 1\nLine 2\nLine 3\nLine 4\nLine 5\nLine 6\nLine 7\nLine 8\nLine 9\nLine 10\n", "", 1)
			commit2 := tester.changeFile("file1.md", content)
			tester.createPR()

			// Create a comment on commit1's "Line 50" change, from the commit-specific view:
			comment := tester.commentFromSpecificCommit(commit1, "file1.md", 50)
			assert.Equal(t, `diff --git a/file1.md b/file1.md
--- a/file1.md
+++ b/file1.md
@@ -48,3 +48,3 @@
 Line 48
 Line 49
-Line 50
+Line 50--modified`, comment.PatchQuoted)
			assert.Equal(t, "proposed", comment.DiffSide())
			assert.EqualValues(t, 50, comment.Line)
			assert.Equal(t, commit1, comment.CommitSHA)

			diff50 := []diffTableRow{
				{rowType: RowHasCode, code: "Line 49"},
				{rowType: RowDelCode, code: "Line 50"},
				{rowType: RowAddCode, code: "Line 50--modified"},
				{rowType: RowComment, commentID: comment.ID},
				{rowType: RowHasCode, code: "Line 51"},
			}
			tester.assertFilesChangedDiff(commit2, diff50)
			tester.assertCommitDiff(commit1, diff50)

			diff10 := []diffTableRow{
				{rowType: RowDelCode, code: "Line 9"},
				{rowType: RowDelCode, code: "Line 10"},
				{rowType: RowHasCode, code: "Line 11"},
			}
			tester.assertFilesChangedDiff(commit2, diff10)
			tester.assertCommitDiff(commit2, diff10)
		})

		t.Run("comment on removed line", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()
			tester := newPullRequestCommentPlacementTester(t)

			// Remove line 50, place a comment on the removed line.
			content := tester.fileContent
			content = strings.Replace(content, "Line 50\n", "", 1)
			commit := tester.changeFile("file1.md", content)
			tester.createPR()

			comment := tester.commentOnPreviousFromFilesChanged("file1.md", 50)
			assert.Equal(t, `diff --git a/file1.md b/file1.md
--- a/file1.md
+++ b/file1.md
@@ -47,7 +47,6 @@ Line 46
 Line 47
 Line 48
 Line 49
-Line 50`, comment.PatchQuoted)
			assert.Equal(t, "previous", comment.DiffSide())
			assert.EqualValues(t, -50, comment.Line)
			assert.Equal(t, tester.initialSHA, comment.CommitSHA) // tracked back to the previous commit where it was line num 50

			diff50 := []diffTableRow{
				{rowType: RowHasCode, code: "Line 49"},
				{rowType: RowDelCode, code: "Line 50"},
				{rowType: RowComment, commentID: comment.ID},
				{rowType: RowHasCode, code: "Line 51"},
			}
			tester.assertFilesChangedDiff(commit, diff50)
			tester.assertCommitDiff(commit, diff50)
		})

		t.Run("comment on removed line moves due to a following commit", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()
			tester := newPullRequestCommentPlacementTester(t)

			// Remove line 50, place a comment on the removed line.
			content := tester.fileContent
			content = strings.Replace(content, "Line 50\n", "", 1)
			commit1 := tester.changeFile("file1.md", content)
			tester.createPR()

			comment := tester.commentOnPreviousFromFilesChanged("file1.md", 50)
			assert.Equal(t, `diff --git a/file1.md b/file1.md
--- a/file1.md
+++ b/file1.md
@@ -47,7 +47,6 @@ Line 46
 Line 47
 Line 48
 Line 49
-Line 50`, comment.PatchQuoted)
			assert.Equal(t, "previous", comment.DiffSide())
			assert.EqualValues(t, -50, comment.Line)
			assert.Equal(t, tester.initialSHA, comment.CommitSHA) // tracked back to the previous commit where it was line num 50

			// Add a second commit to the PR which removes "Line 1" - "Line 10".
			content = strings.Replace(content, "Line 1\nLine 2\nLine 3\nLine 4\nLine 5\nLine 6\nLine 7\nLine 8\nLine 9\nLine 10\n", "", 1)
			commit2 := tester.changeFile("file1.md", content)

			diff2 := []diffTableRow{
				{rowType: RowDelCode, code: "Line 9"},
				{rowType: RowDelCode, code: "Line 10"},
				{rowType: RowHasCode, code: "Line 11"},
			}
			tester.assertFilesChangedDiff(commit2, diff2, "checking commit2 contents in full PR diff")
			tester.assertCommitDiff(commit2, diff2, "checking commit2 contents in single-commit diff")

			diff1 := []diffTableRow{
				{rowType: RowHasCode, code: "Line 49"},
				{rowType: RowDelCode, code: "Line 50"},
				{rowType: RowComment, commentID: comment.ID},
				{rowType: RowHasCode, code: "Line 51"},
			}
			tester.assertFilesChangedDiff(commit2, diff1, "checking commit1 contents in full PR diff")
			tester.assertCommitDiff(commit1, diff1, "checking commit1 contents in single-commit diff")

			// This comment can still be located in the diff, so it should not be marked as Invalidated/Outdated --
			// which is kinda guaranteed by it being loaded in the diff, but for test sanity assert specifically.
			commentReloaded := unittest.AssertExistsAndLoadBean(t, &issues_model.Comment{ID: comment.ID})
			assert.False(t, commentReloaded.Invalidated)
		})

		t.Run("comment on previous side line unchanged by PR appears", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()
			tester := newPullRequestCommentPlacementTester(t)

			// Change line 50
			content := tester.fileContent
			content = strings.Replace(content, "Line 50\n", "Line 50--modified\n", 1)
			commit1 := tester.changeFile("file1.md", content)
			content = strings.Replace(content, "Line 1\nLine 2\nLine 3\nLine 4\nLine 5\nLine 6\nLine 7\nLine 8\nLine 9\nLine 10\n", "", 1)
			commit2 := tester.changeFile("file1.md", content)
			tester.createPR()

			// While viewing the PR, the reviewer made a comment on the previous side on a line of code that wasn't
			// actually changed in the PR:
			comment := tester.commentOnPreviousFromFilesChanged("file1.md", 49)
			assert.Equal(t, `diff --git a/file1.md b/file1.md
--- a/file1.md
+++ b/file1.md
@@ -47,7 +37,7 @@ Line 46
 Line 47
 Line 48
 Line 49`, comment.PatchQuoted)
			assert.Equal(t, "proposed", comment.DiffSide()) // will be moved from previous(LHS)->proposed(RHS) because it wasn't a comment on a change in the PR
			assert.EqualValues(t, 49, comment.Line)
			assert.Equal(t, tester.initialSHA, comment.CommitSHA)

			diff := []diffTableRow{
				{rowType: RowHasCode, code: "Line 49"},
				{rowType: RowComment, commentID: comment.ID},
				{rowType: RowDelCode, code: "Line 50"},
				{rowType: RowAddCode, code: "Line 50--modified"},
				{rowType: RowHasCode, code: "Line 51"},
			}
			tester.assertFilesChangedDiff(commit2, diff, "checking commit1 contents in full PR diff")
			tester.assertCommitDiff(commit1, diff, "checking commit1 contents in single-commit diff")
		})

		t.Run("comment on first line of file removed", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()
			tester := newPullRequestCommentPlacementTester(t)

			// Remove the first line of the file which will then be commented on, as an edge-case
			content := tester.fileContent
			content = strings.Replace(content, "Line 1\n", "", 1)
			commit1 := tester.changeFile("file1.md", content)
			tester.createPR()

			comment := tester.commentOnPreviousFromSpecificCommit(commit1, "file1.md", 1)
			assert.Equal(t, `diff --git a/file1.md b/file1.md
--- a/file1.md
+++ b/file1.md
@@ -1,4 +1,3 @@
-Line 1`, comment.PatchQuoted)
			assert.Equal(t, "previous", comment.DiffSide())
			assert.EqualValues(t, -1, comment.Line)
			assert.Equal(t, tester.initialSHA, comment.CommitSHA)

			diff := []diffTableRow{
				{rowType: RowDelCode, code: "Line 1"},
				{rowType: RowComment, commentID: comment.ID},
				{rowType: RowHasCode, code: "Line 2"},
			}
			tester.assertFilesChangedDiff(commit1, diff, "checking commit1 contents in full PR diff")
			tester.assertCommitDiff(commit1, diff, "checking commit1 contents in single-commit diff")
		})

		t.Run("comment on removed line moves due to a following commit, following commit is rewritten and force-push'd", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()
			tester := newPullRequestCommentPlacementTester(t)

			// Modify line 50
			content := tester.fileContent
			content = strings.Replace(content, "Line 50\n", "Line 50--modified\n", 1)
			commit1 := tester.changeFile("file1.md", content)
			tester.createPR()

			// Place a comment on "Line 50" (removed side)
			comment := tester.commentOnPreviousFromFilesChanged("file1.md", 50)
			assert.Equal(t, `diff --git a/file1.md b/file1.md
--- a/file1.md
+++ b/file1.md
@@ -47,7 +47,7 @@ Line 46
 Line 47
 Line 48
 Line 49
-Line 50`, comment.PatchQuoted)
			assert.Equal(t, "previous", comment.DiffSide())
			assert.EqualValues(t, -50, comment.Line)
			assert.Equal(t, tester.initialSHA, comment.CommitSHA)
			assert.False(t, comment.Invalidated)

			// Add a second commit to the PR which removes  "Line 1" - "Line 10".
			content = strings.Replace(content, "Line 1\nLine 2\nLine 3\nLine 4\nLine 5\nLine 6\nLine 7\nLine 8\nLine 9\nLine 10\n", "", 1)
			tester.changeFile("file1.md", content)

			// Now amend commit2v1 with an additional change, causing a force push of the branch
			forcePushCommit := tester.withBranchCheckout(func(repoPath string) {
				content = strings.Replace(content, "Line 11\n", "", 1) // Remove Line 11 as well
				require.NoError(t, os.WriteFile(path.Join(repoPath, "file1.md"), []byte(content), 0o644))
				require.NoError(t, git.NewCommand(t.Context(), "commit", "-a", "--amend", "--no-edit").Run(&git.RunOpts{Dir: repoPath}))
				require.NoError(t, git.NewCommand(t.Context(), "push", "--force").Run(&git.RunOpts{Dir: repoPath}))
			})

			diff2 := []diffTableRow{
				{rowType: RowDelCode, code: "Line 10"},
				{rowType: RowDelCode, code: "Line 11"},
				{rowType: RowHasCode, code: "Line 12"},
			}
			tester.assertFilesChangedDiff(forcePushCommit, diff2, "checking commit2 (force push) contents in full PR diff")

			diff1 := []diffTableRow{
				{rowType: RowHasCode, code: "Line 49"},
				{rowType: RowDelCode, code: "Line 50"},
				{rowType: RowComment, commentID: comment.ID},
				{rowType: RowAddCode, code: "Line 50--modified"},
				{rowType: RowHasCode, code: "Line 51"},
			}
			tester.assertFilesChangedDiff(forcePushCommit, diff1, "checking commit1 contents in full PR diff")
			tester.assertCommitDiff(commit1, diff1, "checking commit1 contents in single-commit diff")

			// This comment can still be located in the diff, so it should not be marked as Invalidated/Outdated --
			// which is kinda guaranteed by it being loaded in the diff, but for test sanity assert specifically.
			commentReloaded := unittest.AssertExistsAndLoadBean(t, &issues_model.Comment{ID: comment.ID})
			assert.False(t, commentReloaded.Invalidated)
		})

		t.Run("comment on removed line invalidated due to force push", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()
			tester := newPullRequestCommentPlacementTester(t)

			// Modify line 50
			content := tester.fileContent
			content = strings.Replace(content, "Line 50\n", "", 1)
			tester.changeFile("file1.md", content)
			tester.createPR()

			// Place a comment on "Line 50" (removed side)
			comment := tester.commentOnPreviousFromFilesChanged("file1.md", 50)
			assert.Equal(t, `diff --git a/file1.md b/file1.md
--- a/file1.md
+++ b/file1.md
@@ -47,7 +47,6 @@ Line 46
 Line 47
 Line 48
 Line 49
-Line 50`, comment.PatchQuoted)
			assert.Equal(t, "previous", comment.DiffSide())
			assert.EqualValues(t, -50, comment.Line)
			assert.Equal(t, tester.initialSHA, comment.CommitSHA)
			assert.False(t, comment.Invalidated)

			// Now amend commit1 with an additional change that undoes the earlier change, changes something else instead
			forcePushCommit := tester.withBranchCheckout(func(repoPath string) {
				content = strings.Replace(content, "Line 49\n", "Line 49\nLine 50\n", 1)
				content = strings.Replace(content, "Line 52\n", "", 1)
				require.NoError(t, os.WriteFile(path.Join(repoPath, "file1.md"), []byte(content), 0o644))
				require.NoError(t, git.NewCommand(t.Context(), "commit", "-a", "--amend", "--no-edit").Run(&git.RunOpts{Dir: repoPath}))
				require.NoError(t, git.NewCommand(t.Context(), "push", "--force").Run(&git.RunOpts{Dir: repoPath}))
			})

			diff := []diffTableRow{
				{rowType: RowHasCode, code: "Line 49"},
				{rowType: RowHasCode, code: "Line 50"},
				{rowType: RowHasCode, code: "Line 51"},
				{rowType: RowDelCode, code: "Line 52"},
				{rowType: RowHasCode, code: "Line 53"},
			}
			tester.assertFilesChangedDiff(forcePushCommit, diff, "checking commit2 (force push) contents in full PR diff")

			// The comment on "Line 50" can't be valid anymore since that's not in the diff:
			assert.EventuallyWithT(t, func(t *assert.CollectT) {
				commentReloaded := unittest.AssertExistsAndLoadBean(t, &issues_model.Comment{ID: comment.ID})
				assert.True(t, commentReloaded.Invalidated)
			}, 1*time.Second, 50*time.Millisecond)
		})

		t.Run("comment on removed line in specific commit adjusts to correct location", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()
			tester := newPullRequestCommentPlacementTester(t)

			// 3 commits: modify line 50, remove line 50, remove some earlier lines
			content := tester.fileContent
			content = strings.Replace(content, "Line 50\n", "Line 50--modified\n", 1)
			commit1 := tester.changeFile("file1.md", content)
			t.Logf("commit1 = %q", commit1)
			content = strings.Replace(content, "Line 50--modified\n", "", 1)
			commit2 := tester.changeFile("file1.md", content)
			t.Logf("commit2 = %q", commit2)
			content = strings.Replace(content, "Line 1\nLine 2\nLine 3\nLine 4\nLine 5\nLine 6\nLine 7\nLine 8\nLine 9\nLine 10\n", "", 1)
			commit3 := tester.changeFile("file1.md", content)
			t.Logf("commit3 = %q", commit3)
			tester.createPR()

			comment := tester.commentOnPreviousFromSpecificCommit(commit2, "file1.md", 50)
			assert.Equal(t, `diff --git a/file1.md b/file1.md
--- a/file1.md
+++ b/file1.md
@@ -47,7 +47,6 @@ Line 46
 Line 47
 Line 48
 Line 49
-Line 50--modified`, comment.PatchQuoted)
			assert.Equal(t, "previous", comment.DiffSide())
			assert.EqualValues(t, -50, comment.Line)
			assert.Equal(t, commit1, comment.CommitSHA)

			diff1 := []diffTableRow{
				{rowType: RowHasCode, code: "Line 49"},
				{rowType: RowDelCode, code: "Line 50"},
				{rowType: RowAddCode, code: "Line 50--modified"},
				{rowType: RowHasCode, code: "Line 51"},
			}
			tester.assertCommitDiff(commit1, diff1, "checking commit1 contents in single-commit diff")

			diff2 := []diffTableRow{
				{rowType: RowHasCode, code: "Line 49"},
				{rowType: RowDelCode, code: "Line 50--modified"},
				{rowType: RowComment, commentID: comment.ID},
				{rowType: RowHasCode, code: "Line 51"},
			}
			tester.assertCommitDiff(commit2, diff2, "checking commit2 contents in single-commit diff")

			diff3 := []diffTableRow{
				{rowType: RowHasCode, code: "Line 49"},
				{rowType: RowDelCode, code: "Line 50"},
				// This is a small bug -- the comment was placed on the code "Line 50--modified" in commit1, which was
				// later amended by commit2. This comment should be marked out-of-date and not appear here. But the
				// comment's `ResolveCurrentLine` doesn't quite detect this case correctly -- as the comment's CommitSHA
				// is commit1, it performs a diff commit1..PR-HEAD, not mergebase..PR-HEAD, and it believes that this
				// line of code still exists because it exists in that diff range. It's a rare edge case that is defered
				// for future repair.
				{rowType: RowComment, commentID: comment.ID},
				{rowType: RowHasCode, code: "Line 51"},
			}
			tester.assertFilesChangedDiff(commit3, diff3, "checking overall contents in full PR diff")
		})

		t.Run("multi-line comment stores ExtraLinesCount correctly", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()
			tester := newPullRequestCommentPlacementTester(t)

			// Modify lines 48-50
			content := tester.fileContent
			content = strings.Replace(content, "Line 48\n", "Line 48--modified\n", 1)
			content = strings.Replace(content, "Line 49\n", "Line 49--modified\n", 1)
			content = strings.Replace(content, "Line 50\n", "Line 50--modified\n", 1)
			tester.changeFile("file1.md", content)
			tester.createPR()

			// Create a multi-line comment on lines 48-50 (line=48, extraLinesCount=2)
			comment := tester.multiLineCommentFromFilesChanged("file1.md", 48, 2)
			assert.EqualValues(t, 48, comment.Line)
			assert.EqualValues(t, 2, comment.ExtraLinesCount)
			assert.EqualValues(t, 50, comment.UnsignedDisplayLine())
			assert.EqualValues(t, 50, comment.DisplayLine())
			assert.False(t, comment.Invalidated)
		})

		t.Run("reply to a multi-line comment threads under it", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()
			tester := newPullRequestCommentPlacementTester(t)

			content := tester.fileContent
			content = strings.Replace(content, "Line 48\n", "Line 48--modified\n", 1)
			content = strings.Replace(content, "Line 49\n", "Line 49--modified\n", 1)
			content = strings.Replace(content, "Line 50\n", "Line 50--modified\n", 1)
			tester.changeFile("file1.md", content)
			tester.createPR()

			comment := tester.multiLineCommentFromFilesChanged("file1.md", 48, 2)
			assert.EqualValues(t, 2, comment.ExtraLinesCount)
			assert.EqualValues(t, 50, comment.DisplayLine())

			// Simulate a reply like the reply button does
			req := NewRequest(t, "GET", fmt.Sprintf("/%s/%s/pulls/%d/files", tester.repo.OwnerName, tester.repo.Name, tester.pr.Index))
			resp := tester.session.MakeRequest(t, req, http.StatusOK)
			form := NewHTMLParser(t, resp.Body).Find(`.conversation-holder[data-extra-lines-count="2"] form`)
			field := func(name string) string {
				return form.Find(fmt.Sprintf(`input[name=%q]`, name)).AttrOr("value", "")
			}
			require.Equal(t, "2", field("extra_lines_count"), "reply form must carry the parent's extra_lines_count")

			const replyContent = "reply through the multi-line comment reply form"
			req = NewRequestWithValues(t, "POST", fmt.Sprintf("/%s/%s/pulls/%d/files/reviews/comments", tester.repo.OwnerName, tester.repo.Name, tester.pr.Index), map[string]string{
				"origin":            field("origin"),
				"before_commit_id":  field("before_commit_id"),
				"latest_commit_id":  field("latest_commit_id"),
				"side":              field("side"),
				"line":              field("line"),
				"extra_lines_count": field("extra_lines_count"),
				"path":              field("path"),
				"reply":             field("reply"),
				"content":           replyContent,
			})
			tester.session.MakeRequest(t, req, http.StatusOK)

			// The reply threads under the parent: it shares the conversation grouping key
			// (ReviewID + TreePath + DisplayLine, the end of the range).
			reply := unittest.AssertExistsAndLoadBean(t, &issues_model.Comment{Content: replyContent})
			assert.EqualValues(t, 2, reply.ExtraLinesCount)
			assert.Equal(t, comment.ReviewID, reply.ReviewID)
			assert.Equal(t, comment.TreePath, reply.TreePath)
			assert.Equal(t, comment.DisplayLine(), reply.DisplayLine())
		})

		t.Run("multi-line proposed comment over unmodified-then-modified lines stays visible", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()
			tester := newPullRequestCommentPlacementTester(t)

			// Modify line 49 only; line 48 is unmodified. A proposed comment on 48-49 thus spans an
			// unmodified (context) line and a modified (added) line. The whole-range validation must NOT
			// hide it just because line 49 can't be reverse-blamed from line 48's (pre-PR) commit.
			content := strings.Replace(tester.fileContent, "Line 49\n", "Line 49--modified\n", 1)
			commit := tester.changeFile("file1.md", content)
			tester.createPR()

			comment := tester.multiLineCommentFromFilesChanged("file1.md", 48, 1)
			assert.Equal(t, "proposed", comment.DiffSide())
			assert.EqualValues(t, 48, comment.Line)
			assert.EqualValues(t, 1, comment.ExtraLinesCount)

			diff := []diffTableRow{
				{rowType: RowHasCode, code: "Line 48"},
				{rowType: RowDelCode, code: "Line 49"},
				{rowType: RowAddCode, code: "Line 49--modified"},
				{rowType: RowComment, commentID: comment.ID},
				{rowType: RowHasCode, code: "Line 50"},
			}
			tester.assertFilesChangedDiff(commit, diff)
			tester.assertCommitDiff(commit, diff)
		})

		t.Run("multi-line previous comment over only modified lines stays visible", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()
			tester := newPullRequestCommentPlacementTester(t)

			// Modify lines 48-50; on the previous (left) side they appear as removed lines. A previous
			// comment on 48-50 must stay on the left and remain visible (gauche-3).
			content := tester.fileContent
			content = strings.Replace(content, "Line 48\n", "Line 48--modified\n", 1)
			content = strings.Replace(content, "Line 49\n", "Line 49--modified\n", 1)
			content = strings.Replace(content, "Line 50\n", "Line 50--modified\n", 1)
			commit := tester.changeFile("file1.md", content)
			tester.createPR()

			comment := tester.multiLineCommentOnPreviousFromFilesChanged("file1.md", 48, 2)
			assert.Equal(t, "previous", comment.DiffSide())
			assert.EqualValues(t, -48, comment.Line)
			assert.EqualValues(t, 2, comment.ExtraLinesCount)

			diff := []diffTableRow{
				{rowType: RowHasCode, code: "Line 47"},
				{rowType: RowDelCode, code: "Line 48"},
				{rowType: RowDelCode, code: "Line 49"},
				{rowType: RowDelCode, code: "Line 50"},
				{rowType: RowComment, commentID: comment.ID},
				{rowType: RowAddCode, code: "Line 48--modified"},
				{rowType: RowAddCode, code: "Line 49--modified"},
				{rowType: RowAddCode, code: "Line 50--modified"},
				{rowType: RowHasCode, code: "Line 51"},
			}
			tester.assertFilesChangedDiff(commit, diff)
			tester.assertCommitDiff(commit, diff)
		})

		t.Run("multi-line previous comment ending on an unmodified line stays visible", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()
			tester := newPullRequestCommentPlacementTester(t)

			// Modify line 48; line 49 is unmodified. Previous comment on 48-49: first line removed (so it
			// stays on the left), last line unmodified — the display anchor is the unmodified line (gauche-4).
			content := strings.Replace(tester.fileContent, "Line 48\n", "Line 48--modified\n", 1)
			commit := tester.changeFile("file1.md", content)
			tester.createPR()

			comment := tester.multiLineCommentOnPreviousFromFilesChanged("file1.md", 48, 1)
			assert.Equal(t, "previous", comment.DiffSide())
			assert.EqualValues(t, -48, comment.Line)
			assert.EqualValues(t, 1, comment.ExtraLinesCount)

			diff := []diffTableRow{
				{rowType: RowHasCode, code: "Line 47"},
				{rowType: RowDelCode, code: "Line 48"},
				{rowType: RowAddCode, code: "Line 48--modified"},
				{rowType: RowHasCode, code: "Line 49"},
				{rowType: RowComment, commentID: comment.ID},
				{rowType: RowHasCode, code: "Line 50"},
			}
			tester.assertFilesChangedDiff(commit, diff)
			tester.assertCommitDiff(commit, diff)
		})

		t.Run("multi-line previous comment starting on an unmodified line stays previous", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()
			tester := newPullRequestCommentPlacementTester(t)

			// Modify line 49 only; line 48 is unmodified. A previous comment on 48-49 starts on an
			// unmodified line but spans a modified one. It must NOT be converted to a proposed comment
			// just because its first line still exists — it stays on the previous side (gauche-2).
			content := strings.Replace(tester.fileContent, "Line 49\n", "Line 49--modified\n", 1)
			commit := tester.changeFile("file1.md", content)
			tester.createPR()

			comment := tester.multiLineCommentOnPreviousFromFilesChanged("file1.md", 48, 1)
			assert.Equal(t, "previous", comment.DiffSide())
			assert.EqualValues(t, -48, comment.Line)
			assert.EqualValues(t, 1, comment.ExtraLinesCount)

			diff := []diffTableRow{
				{rowType: RowHasCode, code: "Line 47"},
				{rowType: RowHasCode, code: "Line 48"},
				{rowType: RowDelCode, code: "Line 49"},
				{rowType: RowComment, commentID: comment.ID},
				{rowType: RowAddCode, code: "Line 49--modified"},
				{rowType: RowHasCode, code: "Line 50"},
			}
			tester.assertFilesChangedDiff(commit, diff)
			tester.assertCommitDiff(commit, diff)
		})

		t.Run("multi-line previous comment over only unmodified lines becomes proposed", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()
			tester := newPullRequestCommentPlacementTester(t)

			// Modify line 51 (just after the range) so lines 48-50 stay unmodified but appear as context.
			// A previous comment over a fully unmodified range is recorded as a proposed-side comment
			// (matching upstream #12092), since every line still exists at head (gauche-1a).
			content := strings.Replace(tester.fileContent, "Line 51\n", "Line 51--modified\n", 1)
			tester.changeFile("file1.md", content)
			tester.createPR()

			comment := tester.multiLineCommentOnPreviousFromFilesChanged("file1.md", 48, 2)
			assert.Equal(t, "proposed", comment.DiffSide())
			assert.Positive(t, comment.Line)
			assert.EqualValues(t, 2, comment.ExtraLinesCount)
		})

		t.Run("multi-line comment invalidated by DB update", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()
			tester := newPullRequestCommentPlacementTester(t)

			content := tester.fileContent
			content = strings.Replace(content, "Line 48\n", "Line 48--modified\n", 1)
			content = strings.Replace(content, "Line 49\n", "Line 49--modified\n", 1)
			content = strings.Replace(content, "Line 50\n", "Line 50--modified\n", 1)
			tester.changeFile("file1.md", content)
			tester.createPR()

			comment := tester.multiLineCommentFromFilesChanged("file1.md", 48, 2)
			assert.False(t, comment.Invalidated)

			// Manually mark as invalidated (same pattern as existing invalidation tests)
			require.NoError(t, issues_model.UpdateCommentInvalidate(t.Context(), &issues_model.Comment{
				ID:          comment.ID,
				Invalidated: true,
			}))

			// Reload and verify
			updatedComment := unittest.AssertExistsAndLoadBean(t, &issues_model.Comment{ID: comment.ID})
			assert.True(t, updatedComment.Invalidated)
			assert.EqualValues(t, 2, updatedComment.ExtraLinesCount)
		})

		t.Run("multi-line comment invalidated when line inserted in range by second commit", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()
			tester := newPullRequestCommentPlacementTester(t)

			// Modify lines 48-50 in the first commit
			content := tester.fileContent
			content = strings.Replace(content, "Line 48\n", "Line 48--modified\n", 1)
			content = strings.Replace(content, "Line 49\n", "Line 49--modified\n", 1)
			content = strings.Replace(content, "Line 50\n", "Line 50--modified\n", 1)
			tester.changeFile("file1.md", content)
			tester.createPR()

			// Create a multi-line comment on lines 48-50 (line=48, extraLinesCount=2)
			comment := tester.multiLineCommentFromFilesChanged("file1.md", 48, 2)
			assert.EqualValues(t, 48, comment.Line)
			assert.EqualValues(t, 2, comment.ExtraLinesCount)
			assert.False(t, comment.Invalidated)

			// Push a second commit that inserts a new line between 48 and 49,
			// breaking the contiguity of the range.
			content = strings.Replace(content, "Line 48--modified\n", "Line 48--modified\nInserted line\n", 1)
			tester.changeFile("file1.md", content)

			// The invalidation is triggered asynchronously when the branch is pushed.
			// The comment should eventually be invalidated because the range [48, 49, 50]
			// resolves to non-contiguous lines at HEAD (48, 50, 51 — with a gap at 49).
			assert.EventuallyWithT(t, func(t *assert.CollectT) {
				commentReloaded := unittest.AssertExistsAndLoadBean(t, &issues_model.Comment{ID: comment.ID})
				assert.True(t, commentReloaded.Invalidated)
			}, 5*time.Second, 50*time.Millisecond)
		})

		t.Run("multi-line comment not invalidated when range stays contiguous", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()
			tester := newPullRequestCommentPlacementTester(t)

			// Modify lines 48-50 in the first commit
			content := tester.fileContent
			content = strings.Replace(content, "Line 48\n", "Line 48--modified\n", 1)
			content = strings.Replace(content, "Line 49\n", "Line 49--modified\n", 1)
			content = strings.Replace(content, "Line 50\n", "Line 50--modified\n", 1)
			tester.changeFile("file1.md", content)
			tester.createPR()

			// Create a multi-line comment on lines 48-50 (line=48, extraLinesCount=2)
			comment := tester.multiLineCommentFromFilesChanged("file1.md", 48, 2)
			assert.EqualValues(t, 48, comment.Line)
			assert.EqualValues(t, 2, comment.ExtraLinesCount)
			assert.False(t, comment.Invalidated)

			// Push a second commit that changes lines BEFORE the range (removing lines 1-10),
			// which shifts the range but keeps it contiguous.
			content = strings.Replace(content, "Line 1\nLine 2\nLine 3\nLine 4\nLine 5\nLine 6\nLine 7\nLine 8\nLine 9\nLine 10\n", "", 1)
			newSHA := tester.changeFile("file1.md", content)

			// Run the invalidation pass synchronously instead of waiting for the async
			// goroutine, then check the comment is still valid.
			pr, err := issues_model.GetPullRequestByIndex(t.Context(), tester.repo.ID, tester.pr.Index)
			require.NoError(t, err)
			require.NoError(t, pull_service.InvalidateCodeComments(t.Context(),
				issues_model.PullRequestList{pr}, tester.user, tester.repo, newSHA))

			commentReloaded := unittest.AssertExistsAndLoadBean(t, &issues_model.Comment{ID: comment.ID})
			assert.False(t, commentReloaded.Invalidated)

			// Verify the comment is visible in the files changed diff (not skipped).
			// The full PR diff (mergebase→HEAD) groups deletions then additions for modified lines.
			diff := []diffTableRow{
				{rowType: RowHasCode, code: "Line 47"},
				{rowType: RowDelCode, code: "Line 48"},
				{rowType: RowDelCode, code: "Line 49"},
				{rowType: RowDelCode, code: "Line 50"},
				{rowType: RowAddCode, code: "Line 48--modified"},
				{rowType: RowAddCode, code: "Line 49--modified"},
				{rowType: RowAddCode, code: "Line 50--modified"},
				{rowType: RowComment, commentID: comment.ID},
				{rowType: RowHasCode, code: "Line 51"},
			}
			tester.assertFilesChangedDiff(newSHA, diff)
		})

		// Helper: comment on lines 48-50 (anchor 48, middle 49, last 50, all modified by the PR), then a
		// later commit applies `secondCommit` to the file and we check the resulting invalidation state.
		runRangeInvalidation := func(t *testing.T, secondCommit func(content string) string, wantInvalidated bool) {
			tester := newPullRequestCommentPlacementTester(t)

			content := tester.fileContent
			content = strings.Replace(content, "Line 48\n", "Line 48--modified\n", 1)
			content = strings.Replace(content, "Line 49\n", "Line 49--modified\n", 1)
			content = strings.Replace(content, "Line 50\n", "Line 50--modified\n", 1)
			tester.changeFile("file1.md", content)
			tester.createPR()

			comment := tester.multiLineCommentFromFilesChanged("file1.md", 48, 2)
			assert.EqualValues(t, 48, comment.Line)
			assert.EqualValues(t, 2, comment.ExtraLinesCount)
			assert.False(t, comment.Invalidated)

			newSHA := tester.changeFile("file1.md", secondCommit(content))

			if wantInvalidated {
				assert.EventuallyWithT(t, func(t *assert.CollectT) {
					commentReloaded := unittest.AssertExistsAndLoadBean(t, &issues_model.Comment{ID: comment.ID})
					assert.True(t, commentReloaded.Invalidated)
				}, 5*time.Second, 50*time.Millisecond)
			} else {
				// Run the invalidation pass synchronously instead of waiting for the async
				// goroutine, then assert the comment stayed valid.
				pr, err := issues_model.GetPullRequestByIndex(t.Context(), tester.repo.ID, tester.pr.Index)
				require.NoError(t, err)
				require.NoError(t, pull_service.InvalidateCodeComments(t.Context(),
					issues_model.PullRequestList{pr}, tester.user, tester.repo, newSHA))

				commentReloaded := unittest.AssertExistsAndLoadBean(t, &issues_model.Comment{ID: comment.ID})
				assert.False(t, commentReloaded.Invalidated)
			}
		}

		t.Run("multi-line comment invalidated when a middle line is deleted by a later commit", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()
			runRangeInvalidation(t, func(content string) string {
				return strings.Replace(content, "Line 49--modified\n", "", 1)
			}, true)
		})

		t.Run("multi-line comment invalidated when a middle line content changes in a later commit", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()
			runRangeInvalidation(t, func(content string) string {
				return strings.Replace(content, "Line 49--modified\n", "Line 49--changed-again\n", 1)
			}, true)
		})

		t.Run("multi-line comment invalidated when the last line content changes in a later commit", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()
			runRangeInvalidation(t, func(content string) string {
				return strings.Replace(content, "Line 50--modified\n", "Line 50--changed-again\n", 1)
			}, true)
		})

		t.Run("multi-line comment not invalidated when a line outside the range changes", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()
			runRangeInvalidation(t, func(content string) string {
				return strings.Replace(content, "Line 60\n", "Line 60--modified\n", 1)
			}, false)
		})

		t.Run("multi-line comment invalidated when the last line is deleted by a later commit", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()
			runRangeInvalidation(t, func(content string) string {
				return strings.Replace(content, "Line 50--modified\n", "", 1)
			}, true)
		})

		t.Run("multi-line comment invalidated when the first (anchor) line content changes in a later commit", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()
			runRangeInvalidation(t, func(content string) string {
				return strings.Replace(content, "Line 48--modified\n", "Line 48--changed-again\n", 1)
			}, true)
		})

		t.Run("multi-line comment invalidated when the first (anchor) line is deleted by a later commit", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()
			runRangeInvalidation(t, func(content string) string {
				return strings.Replace(content, "Line 48--modified\n", "", 1)
			}, true)
		})

		t.Run("reply to review on added change", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()
			tester := newPullRequestCommentPlacementTester(t)

			// Modify line 50
			content := tester.fileContent
			content = strings.Replace(content, "Line 50\n", "Line 50--modified\n", 1)
			commit1 := tester.changeFile("file1.md", content)
			tester.createPR()

			// Place a comment on "Line 50--modified"
			comment := tester.commentFromFilesChanged("file1.md", 50)
			assert.Equal(t, `diff --git a/file1.md b/file1.md
--- a/file1.md
+++ b/file1.md
@@ -48,3 +48,3 @@
 Line 48
 Line 49
-Line 50
+Line 50--modified`, comment.PatchQuoted)
			assert.Equal(t, "proposed", comment.DiffSide())
			assert.EqualValues(t, 50, comment.Line)
			assert.Equal(t, commit1, comment.CommitSHA)
			assert.False(t, comment.Invalidated)

			// Reply to the comment on "Line 50--modified"
			reply := tester.replyToComment()
			assert.NotEqual(t, comment.ID, reply.ID)
			assert.Equal(t, comment.DiffSide(), reply.DiffSide())
			assert.Equal(t, comment.Line, reply.Line)
			assert.Equal(t, comment.CommitSHA, reply.CommitSHA)
		})

		t.Run("reply to review on removed change", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()
			tester := newPullRequestCommentPlacementTester(t)

			// Modify line 50
			content := tester.fileContent
			content = strings.Replace(content, "Line 50\n", "Line 50--modified\n", 1)
			tester.changeFile("file1.md", content)
			tester.createPR()

			// Place a comment on removed "Line 50"
			comment := tester.commentOnPreviousFromFilesChanged("file1.md", 50)
			assert.Equal(t, `diff --git a/file1.md b/file1.md
--- a/file1.md
+++ b/file1.md
@@ -47,7 +47,7 @@ Line 46
 Line 47
 Line 48
 Line 49
-Line 50`, comment.PatchQuoted)
			assert.Equal(t, "previous", comment.DiffSide())
			assert.EqualValues(t, -50, comment.Line)
			assert.Equal(t, tester.initialSHA, comment.CommitSHA)
			assert.False(t, comment.Invalidated)

			// Reply to the comment on "-Line 50"
			reply := tester.replyToComment()
			assert.NotEqual(t, comment.ID, reply.ID)
			assert.Equal(t, comment.DiffSide(), reply.DiffSide())
			assert.Equal(t, comment.Line, reply.Line)
			assert.Equal(t, comment.CommitSHA, reply.CommitSHA)
		})
	})
}

type PullRequestCommentPlacementTester struct {
	t           *testing.T
	user        *user_model.User
	session     *TestSession
	apiToken    string
	fileContent string
	repo        *repo_model.Repository
	initialSHA  string
	branch      optional.Option[string]
	pr          *api.PullRequest
}

func newPullRequestCommentPlacementTester(t *testing.T) *PullRequestCommentPlacementTester {
	user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	token := getUserToken(t, "user2", auth_model.AccessTokenScopeWriteRepository, auth_model.AccessTokenScopeWriteIssue)
	session := loginUser(t, user2.Name)

	var content strings.Builder
	for i := range 100 {
		content.WriteString(fmt.Sprintf("Line %d\n", i+1)) // +1 -> make "Line N" appear on the Nth line and avoid off-by-one confusions
	}

	var initialSHA string
	repo := forgery.CreateRepository(t, user2, &forgery.CreateRepositoryOptions{
		Files: forgery.MapFS{
			"file1.md": forgery.MapFile(content.String()),
			"file2.md": forgery.MapFile(content.String()),
		},
		LatestSha: &initialSHA,
	})

	return &PullRequestCommentPlacementTester{
		t:           t,
		user:        user2,
		session:     session,
		apiToken:    token,
		fileContent: content.String(),
		repo:        repo,
		initialSHA:  initialSHA,
	}
}

func (tester *PullRequestCommentPlacementTester) changeFileOnBranch(sourceBranch, targetBranch string, targetBranchIsNew bool, filename, newContent string) string {
	req := NewRequest(tester.t,
		"GET",
		fmt.Sprintf("/api/v1/repos/%s/%s/contents/%s?ref=%s", tester.repo.OwnerName, tester.repo.Name, filename, sourceBranch)).
		AddTokenAuth(tester.apiToken)
	resp := MakeRequest(tester.t, req, http.StatusOK)
	var existingFile api.ContentsResponse
	DecodeJSON(tester.t, resp, &existingFile)

	opts := api.UpdateFileOptions{
		DeleteFileOptions: api.DeleteFileOptions{
			SHA: existingFile.SHA,
		},
		ContentBase64: base64.StdEncoding.EncodeToString([]byte(newContent)),
	}
	if targetBranchIsNew {
		opts.DeleteFileOptions.FileOptions.NewBranchName = targetBranch
	} else {
		opts.DeleteFileOptions.FileOptions.BranchName = targetBranch
	}
	req = NewRequestWithJSON(tester.t,
		"PUT",
		fmt.Sprintf("/api/v1/repos/%s/%s/contents/%s", tester.repo.OwnerName, tester.repo.Name, filename),
		opts).AddTokenAuth(tester.apiToken)
	resp = MakeRequest(tester.t, req, http.StatusOK)
	var updateFileResponse api.FileResponse
	DecodeJSON(tester.t, resp, &updateFileResponse)

	return updateFileResponse.Commit.SHA
}

func (tester *PullRequestCommentPlacementTester) changeFileOnBase(filename, newContent string) string {
	return tester.changeFileOnBranch(tester.repo.DefaultBranch, tester.repo.DefaultBranch, false, filename, newContent)
}

func (tester *PullRequestCommentPlacementTester) changeFile(filename, newContent string) string {
	var sourceBranch string // where to get the file's last SHA from
	branchExists, branch := tester.branch.Get()
	if !branchExists {
		branch = fmt.Sprintf("branch-%s", uuid.New().String())
		tester.branch = optional.Some(branch)
		sourceBranch = tester.repo.DefaultBranch
	} else {
		sourceBranch = branch
	}
	return tester.changeFileOnBranch(sourceBranch, branch, !branchExists, filename, newContent)
}

func (tester *PullRequestCommentPlacementTester) createPR() {
	branchExists, branch := tester.branch.Get()
	require.True(tester.t, branchExists)
	req := NewRequestWithJSON(tester.t, "POST",
		fmt.Sprintf("/api/v1/repos/%s/%s/pulls", tester.repo.OwnerName, tester.repo.Name),
		&api.CreatePullRequestOption{
			Head:  branch,
			Base:  tester.repo.DefaultBranch,
			Title: fmt.Sprintf("PR from branch %s", tester.branch),
		}).AddTokenAuth(tester.apiToken)
	resp := MakeRequest(tester.t, req, http.StatusCreated)
	var pr api.PullRequest
	DecodeJSON(tester.t, resp, &pr)
	tester.pr = &pr
}

func (tester *PullRequestCommentPlacementTester) commentFromFilesChanged(filename string, line int) *issues_model.Comment {
	req := NewRequest(tester.t, "GET",
		// omit after_commit_id -- new_comment form defaults to fetching the PR head
		fmt.Sprintf("/%s/%s/pulls/%d/files/reviews/new_comment", tester.repo.OwnerName, tester.repo.Name, tester.pr.Index))
	resp := tester.session.MakeRequest(tester.t, req, http.StatusOK)
	return tester.commentFromNewCommentForm(resp, filename, line, "proposed")
}

func (tester *PullRequestCommentPlacementTester) commentOnPreviousFromFilesChanged(filename string, line int) *issues_model.Comment {
	req := NewRequest(tester.t, "GET",
		// omit after_commit_id -- new_comment form defaults to fetching the PR head
		fmt.Sprintf("/%s/%s/pulls/%d/files/reviews/new_comment", tester.repo.OwnerName, tester.repo.Name, tester.pr.Index))
	resp := tester.session.MakeRequest(tester.t, req, http.StatusOK)
	return tester.commentFromNewCommentForm(resp, filename, line, "previous")
}

func (tester *PullRequestCommentPlacementTester) multiLineCommentOnPreviousFromFilesChanged(filename string, line, extraLinesCount int) *issues_model.Comment {
	req := NewRequest(tester.t, "GET",
		fmt.Sprintf("/%s/%s/pulls/%d/files/reviews/new_comment", tester.repo.OwnerName, tester.repo.Name, tester.pr.Index))
	resp := tester.session.MakeRequest(tester.t, req, http.StatusOK)
	return tester.commentFromNewCommentFormWithExtraLinesCount(resp, filename, line, extraLinesCount, "previous")
}

func (tester *PullRequestCommentPlacementTester) replyToComment() *issues_model.Comment {
	// Posts a reply to a comment from the "Conversation" tab of a pull request.  Requires that there is only one
	// comment on the pull request currently, as it pulls values out of the "Reply" <form> without any support for
	// having zero or 1+ forms rendered in the page.

	req := NewRequest(tester.t, "GET",
		fmt.Sprintf("/%s/%s/pulls/%d", tester.repo.OwnerName, tester.repo.Name, tester.pr.Index))
	resp := tester.session.MakeRequest(tester.t, req, http.StatusOK)
	doc := NewHTMLParser(tester.t, resp.Body)

	// Mostly the same as commentFromNewCommentFormWithExtraLinesCount, except almost all values come from the hidden
	// form inputs on the PR page, and it's a reply rather than a single_review comment.
	commentContent := uuid.New().String()
	req = NewRequestWithValues(tester.t, "POST",
		fmt.Sprintf("/%s/%s/pulls/%d/files/reviews/comments", tester.repo.OwnerName, tester.repo.Name, tester.pr.Index),
		map[string]string{
			"origin":            doc.GetInputValueByName("origin"),
			"before_commit_id":  doc.GetInputValueByName("before_commit_id"),
			"latest_commit_id":  doc.GetInputValueByName("latest_commit_id"),
			"side":              doc.GetInputValueByName("side"),
			"line":              doc.GetInputValueByName("line"),
			"extra_lines_count": doc.GetInputValueByName("extra_lines_count"),
			"path":              doc.GetInputValueByName("path"),
			"diff_start_cid":    doc.GetInputValueByName("diff_start_cid"),
			"diff_end_cid":      doc.GetInputValueByName("diff_end_cid"),
			"diff_base_cid":     doc.GetInputValueByName("diff_base_cid"),
			"reply":             doc.GetInputValueByName("reply"), // comment ID we're replying to
			"content":           commentContent,
			"single_review":     "true",
		})
	tester.session.MakeRequest(tester.t, req, http.StatusOK)

	comment := unittest.AssertExistsAndLoadBean(tester.t, &issues_model.Comment{Content: commentContent})
	return comment
}

func (tester *PullRequestCommentPlacementTester) getCommitParent(commitID string) string {
	repo, err := gitrepo.OpenRepository(tester.t.Context(), tester.repo)
	require.NoError(tester.t, err)
	defer repo.Close()
	commit, err := repo.GetCommit(commitID)
	require.NoError(tester.t, err)
	require.Len(tester.t, commit.Parents, 1)
	return commit.Parents[0].String()
}

func (tester *PullRequestCommentPlacementTester) commentFromSpecificCommit(commitID, filename string, line int) *issues_model.Comment {
	beforeCommitID := tester.getCommitParent(commitID)
	req := NewRequest(tester.t, "GET",
		fmt.Sprintf("/%s/%s/pulls/%d/files/reviews/new_comment?before_commit_id=%s&after_commit_id=%s", tester.repo.OwnerName, tester.repo.Name, tester.pr.Index, beforeCommitID, commitID))
	resp := tester.session.MakeRequest(tester.t, req, http.StatusOK)
	return tester.commentFromNewCommentForm(resp, filename, line, "proposed")
}

func (tester *PullRequestCommentPlacementTester) commentOnPreviousFromSpecificCommit(commitID, filename string, line int) *issues_model.Comment {
	beforeCommitID := tester.getCommitParent(commitID)
	tester.t.Logf("beforeCommitID(%q) = %q", commitID, beforeCommitID)
	req := NewRequest(tester.t, "GET",
		fmt.Sprintf("/%s/%s/pulls/%d/files/reviews/new_comment?before_commit_id=%s&after_commit_id=%s", tester.repo.OwnerName, tester.repo.Name, tester.pr.Index, beforeCommitID, commitID))
	resp := tester.session.MakeRequest(tester.t, req, http.StatusOK)
	return tester.commentFromNewCommentForm(resp, filename, line, "previous")
}

func (tester *PullRequestCommentPlacementTester) commentFromNewCommentForm(resp *httptest.ResponseRecorder, filename string, line int, side string) *issues_model.Comment {
	return tester.commentFromNewCommentFormWithExtraLinesCount(resp, filename, line, 0, side)
}

func (tester *PullRequestCommentPlacementTester) commentFromNewCommentFormWithExtraLinesCount(resp *httptest.ResponseRecorder, filename string, line, extraLinesCount int, side string) *issues_model.Comment {
	commentContent := uuid.New().String()
	doc := NewHTMLParser(tester.t, resp.Body)
	tester.t.Logf("doc.before = %q", doc.GetInputValueByName("before_commit_id"))
	tester.t.Logf("doc.latest = %q", doc.GetInputValueByName("latest_commit_id"))
	req := NewRequestWithValues(tester.t, "POST",
		fmt.Sprintf("/%s/%s/pulls/%d/files/reviews/comments", tester.repo.OwnerName, tester.repo.Name, tester.pr.Index),
		map[string]string{
			"origin":            doc.GetInputValueByName("origin"),
			"before_commit_id":  doc.GetInputValueByName("before_commit_id"),
			"latest_commit_id":  doc.GetInputValueByName("latest_commit_id"),
			"side":              side,
			"line":              strconv.Itoa(line),
			"extra_lines_count": strconv.Itoa(extraLinesCount),
			"path":              filename,
			"diff_start_cid":    doc.GetInputValueByName("diff_start_cid"),
			"diff_end_cid":      doc.GetInputValueByName("diff_end_cid"),
			"diff_base_cid":     doc.GetInputValueByName("diff_base_cid"),
			"content":           commentContent,
			"single_review":     "true",
		})
	tester.session.MakeRequest(tester.t, req, http.StatusOK)

	comment := unittest.AssertExistsAndLoadBean(tester.t, &issues_model.Comment{Content: commentContent})
	return comment
}

func (tester *PullRequestCommentPlacementTester) multiLineCommentFromFilesChanged(filename string, line, extraLinesCount int) *issues_model.Comment {
	req := NewRequest(tester.t, "GET",
		fmt.Sprintf("/%s/%s/pulls/%d/files/reviews/new_comment", tester.repo.OwnerName, tester.repo.Name, tester.pr.Index))
	resp := tester.session.MakeRequest(tester.t, req, http.StatusOK)
	return tester.commentFromNewCommentFormWithExtraLinesCount(resp, filename, line, extraLinesCount, "proposed")
}

func (tester *PullRequestCommentPlacementTester) withBranchCheckout(action func(string)) string {
	dstPath := tester.t.TempDir()
	cloneURL, _ := url.Parse(tester.repo.CloneLink().HTTPS)
	cloneURL.User = url.UserPassword(tester.user.LoginName, userPassword)
	require.NoError(tester.t, git.CloneWithArgs(tester.t.Context(), nil, cloneURL.String(), dstPath, git.CloneRepoOptions{}))
	doGitSetRemoteURL(dstPath, "origin", cloneURL)(tester.t)

	branchExists, branch := tester.branch.Get()
	require.True(tester.t, branchExists)
	require.NoError(tester.t, git.NewCommand(tester.t.Context(), "checkout").AddDynamicArguments(branch).Run(&git.RunOpts{Dir: dstPath}))

	action(dstPath)

	commit, err := git.GetFullCommitID(tester.t.Context(), dstPath, "HEAD")
	require.NoError(tester.t, err)
	return commit
}

func (tester *PullRequestCommentPlacementTester) assertFilesChangedDiff(expectedCommitID string, rowAssertions []diffTableRow, note ...string) {
	fetchPage := func() *HTMLDoc {
		req := NewRequest(tester.t, "GET",
			fmt.Sprintf("/%s/%s/pulls/%d/files", tester.repo.OwnerName, tester.repo.Name, tester.pr.Index))
		resp := tester.session.MakeRequest(tester.t, req, http.StatusOK)
		return NewHTMLParser(tester.t, resp.Body)
	}

	var doc *HTMLDoc
	if expectedCommitID != "" {
		// In services/pull/pull.go, `TestPullRequest` (which is, to be clear, not a test) will spawn a concurrent
		// goroutine during the post-receive hook.  This goroutine is responsible for updating the refs/pull/%d/head
		// reference in the repo, which is what the UI uses in order to render the pull request diff page.  Because this
		// routine is concurrent, occasionally the test will reach this point and it won't yet be completed.
		// Experimental testing shows that it completes very fast (under a 1 second retry) on CI, so here we give it 10
		// seconds to reach the expected commit ID, reloading the page each time.
		require.EventuallyWithT(tester.t, func(collect *assert.CollectT) {
			doc = fetchPage()
			commitIDInput := doc.Find("input[name=commit_id]")
			uiCommitID, exists := commitIDInput.Attr("value")
			require.True(collect, exists)
			assert.Equal(collect, expectedCommitID, uiCommitID)
		}, 10*time.Second, time.Second, "expected to find PR diff page with commit ID %s", expectedCommitID)
	} else {
		// For some tests we don't know the expected commit ID for rendering the page, typically when a force push is
		// used and we're not using Forgejo's API which provides the commit ID.  No retry loop here.
		doc = fetchPage()
	}

	var testNote string
	if len(note) == 0 {
		testNote = "contents in single-commit diff"
	} else {
		testNote = note[0]
	}
	assertDiffTable(tester.t, doc, rowAssertions, testNote)
}

func (tester *PullRequestCommentPlacementTester) assertCommitDiff(commitSHA string, rowAssertions []diffTableRow, note ...string) {
	req := NewRequest(tester.t, "GET",
		fmt.Sprintf("/%s/%s/pulls/%d/commits/%s", tester.repo.OwnerName, tester.repo.Name, tester.pr.Index, commitSHA))
	resp := tester.session.MakeRequest(tester.t, req, http.StatusOK)
	doc := NewHTMLParser(tester.t, resp.Body)
	var testNote string
	if len(note) == 0 {
		testNote = "contents in full PR diff"
	} else {
		testNote = note[0]
	}
	assertDiffTable(tester.t, doc, rowAssertions, testNote)
}

func lineNumber(content, line string) int {
	return slices.Index(strings.Split(content, "\n"), line) + 1
}

type diffTableRowType int

const (
	RowHasCode diffTableRowType = iota
	RowAddCode
	RowDelCode
	RowComment
)

type diffTableRow struct {
	rowType diffTableRowType
	// RowHasCode, RowAddCode, RowDelCode
	code string
	// RowComment
	commentID int64
}

func nodeText(node *html.Node) string {
	if node.Type == html.TextNode {
		return node.Data
	}
	var builder strings.Builder
	for child := range node.ChildNodes() {
		childText := strings.TrimSpace(nodeText(child))
		builder.WriteString(childText)
	}
	return builder.String()
}

func nodeAttr(node *html.Node, key string) string {
	for _, attr := range node.Attr {
		if attr.Key == key {
			return attr.Val
		}
	}
	return ""
}

func checkDiffTableRow(t *testing.T, tableRow *html.Node, rowAssertion diffTableRow) string {
	switch rowAssertion.rowType {
	case RowHasCode:
		text := nodeText(tableRow)
		if text != rowAssertion.code {
			return fmt.Sprintf("wanted diff %q, but found diff %q", rowAssertion.code, text)
		}
	case RowDelCode:
		dataLineType := nodeAttr(tableRow, "data-line-type")
		if dataLineType != "del" {
			return fmt.Sprintf("wanted delete code in diff, but found data-line-type=%q", dataLineType)
		}
		text := nodeText(tableRow)
		if text != rowAssertion.code {
			return fmt.Sprintf("wanted delete code with line %q, but found diff %q", rowAssertion.code, text)
		}
	case RowAddCode:
		dataLineType := nodeAttr(tableRow, "data-line-type")
		if dataLineType != "add" {
			return fmt.Sprintf("wanted add code in diff, but found data-line-type=%q", dataLineType)
		}
		text := nodeText(tableRow)
		if text != rowAssertion.code {
			return fmt.Sprintf("wanted add code with line %q, but found diff %q", rowAssertion.code, text)
		}
	case RowComment:
		class := nodeAttr(tableRow, "class")
		if class != "add-comment" {
			return fmt.Sprintf("wanted comment in diff, but found class=%q", class)
		}
		found := false
		for desc := range tableRow.Descendants() {
			descID := nodeAttr(desc, "id")
			if descID == fmt.Sprintf("code-comments-%d", rowAssertion.commentID) {
				found = true
				break
			}
		}
		if !found {
			return fmt.Sprintf("wanted comment with ID %d, but could not be identified", rowAssertion.commentID)
		}
	}
	return ""
}

func assertDiffTable(t *testing.T, doc *HTMLDoc, rowAssertions []diffTableRow, note string) {
	require.NotEmpty(t, rowAssertions)

	diffTable := doc.Find("table.chroma")
	require.Equal(t, 1, diffTable.Length())

	rows := diffTable.Find("tbody > tr[data-line-type]") // [data-line-type] is used to avoid matching tables within comment boxes

	// Find the first row to match rowAssertions[0], and then we'll iterate from there matching each row exactly.
	tableFirstRowIndex := 0
	foundFirst := false
	firstRowMismatches := []string{}
	for ; tableFirstRowIndex < rows.Length(); tableFirstRowIndex++ {
		mismatch := checkDiffTableRow(t, rows.Get(tableFirstRowIndex), rowAssertions[0])
		if mismatch == "" {
			foundFirst = true
			break
		}
		firstRowMismatches = append(firstRowMismatches, mismatch)
	}
	if !foundFirst {
		// We're going to fail because we couldn't find the first row in rowAssertions -- this can be tricky to debug so
		// help out by outputting all the rows we looked at that didn't match:
		t.Log("first row mismatches:")
		for _, mm := range firstRowMismatches {
			t.Logf("\t%s", mm)
		}
		require.Failf(t, "unable to find first row", "test %s: failed to find first row assertion", note)
	}

	for idx, assertion := range rowAssertions {
		if idx == 0 { // skip first row assertion, already checked to find tableFirstRowIndex
			continue
		}

		tableIdx := tableFirstRowIndex + idx
		if tableIdx >= rows.Length() {
			require.Failf(t, "ran out of table rows", "test %s: row assertion at index %d couldn't be satisfied", note, idx)
		}

		tableRow := rows.Get(tableIdx)
		check := checkDiffTableRow(t, tableRow, assertion)
		if check != "" {
			assert.Failf(t, check, "test %s: row assertion at index %d couldn't be satisfied", note, idx)
		}
	}
}
