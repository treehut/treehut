// Copyright 2024 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"net/http"
	"testing"

	"forgejo.org/models/db"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unit"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/tests"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestViewPulls(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	req := NewRequest(t, "GET", "/user2/repo1/pulls")
	resp := MakeRequest(t, req, http.StatusOK)

	htmlDoc := NewHTMLParser(t, resp.Body)
	search := htmlDoc.doc.Find(".list-header-search > .search > .input > input")
	placeholder, _ := search.Attr("placeholder")
	assert.Equal(t, "Search pulls…", placeholder)
}

func TestPullViewConversation(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	req := NewRequest(t, "GET", "/user2/commitsonpr/pulls/1")
	resp := MakeRequest(t, req, http.StatusOK)

	htmlDoc := NewHTMLParser(t, resp.Body)

	t.Run("Commits", func(t *testing.T) {
		commitLists := htmlDoc.Find(".timeline-item.commits-list")
		assert.Equal(t, 4, commitLists.Length())

		commits := commitLists.Find(".singular-commit")
		assert.Equal(t, 10, commits.Length())

		// First one has not been affected by a force push, therefore it's still part of the
		// PR and should link to the PR-scoped review tab
		firstCommit := commits.Eq(0)
		firstCommitMessageHref, _ := firstCommit.Find("a.default-link").Attr("href")
		firstCommitShaHref, _ := firstCommit.Find("a.sha.label").Attr("href")
		assert.Equal(t, "/user2/commitsonpr/pulls/1/commits/4ca8bcaf27e28504df7bf996819665986b01c847", firstCommitMessageHref)
		assert.Equal(t, "/user2/commitsonpr/pulls/1/commits/4ca8bcaf27e28504df7bf996819665986b01c847", firstCommitShaHref)

		// The fifth commit has been overwritten by a force push.
		// Attempting to view the old one in the review tab won't work:
		req := NewRequest(t, "GET", "/user2/commitsonpr/pulls/1/commits/3e64625bd6eb5bcba69ac97de6c8f507402df861")
		MakeRequest(t, req, http.StatusNotFound)

		// Therefore, this commit should link to the non-PR commit view instead
		fifthCommit := commits.Eq(4)
		fifthCommitMessageHref, _ := fifthCommit.Find("a.default-link").Attr("href")
		fifthCommitShaHref, _ := fifthCommit.Find("a.sha.label").Attr("href")
		assert.Equal(t, "/user2/commitsonpr/commit/3e64625bd6eb5bcba69ac97de6c8f507402df861", fifthCommitMessageHref)
		assert.Equal(t, "/user2/commitsonpr/commit/3e64625bd6eb5bcba69ac97de6c8f507402df861", fifthCommitShaHref)
	})
}

func TestPullManuallyMergeWarning(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	session := loginUser(t, user2.Name)

	warningMessage := `Warning: The "Autodetect manual merge" setting is not enabled for this repository, you will have to mark this pull request as manually merged afterwards.`
	t.Run("Autodetect disabled", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		req := NewRequest(t, "GET", "/user2/repo1/pulls/3")
		resp := session.MakeRequest(t, req, http.StatusOK)

		htmlDoc := NewHTMLParser(t, resp.Body)
		mergeInstructions := htmlDoc.Find("#merge-instructions").Text()
		assert.Contains(t, mergeInstructions, warningMessage)
	})

	pullRequestUnit := unittest.AssertExistsAndLoadBean(t, &repo_model.RepoUnit{RepoID: 1, Type: unit.TypePullRequests})
	config := pullRequestUnit.PullRequestsConfig()
	config.AutodetectManualMerge = true
	_, err := db.GetEngine(db.DefaultContext).ID(pullRequestUnit.ID).Cols("config").Update(pullRequestUnit)
	require.NoError(t, err)

	t.Run("Autodetect enabled", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		req := NewRequest(t, "GET", "/user2/repo1/pulls/3")
		resp := session.MakeRequest(t, req, http.StatusOK)

		htmlDoc := NewHTMLParser(t, resp.Body)
		mergeInstructions := htmlDoc.Find("#merge-instructions").Text()
		assert.NotContains(t, mergeInstructions, warningMessage)
	})
}

func TestPullCombinedReviewRequest(t *testing.T) {
	defer unittest.OverrideFixtures("tests/integration/fixtures/TestPullCombinedReviewRequest")()
	defer tests.PrepareTestEnv(t)()

	session := loginUser(t, "user2")

	helper := func(t *testing.T, action, userID, expectedText string) {
		t.Helper()

		req := NewRequestWithValues(t, "POST", "/user2/repo1/pulls/request_review", map[string]string{
			"_csrf":     GetCSRF(t, session, "/user2/repo1/pulls/3"),
			"issue_ids": "3",
			"action":    action,
			"id":        userID,
		})
		session.MakeRequest(t, req, http.StatusOK)

		req = NewRequest(t, "GET", "/user2/repo1/pulls/3")
		resp := session.MakeRequest(t, req, http.StatusOK)
		htmlDoc := NewHTMLParser(t, resp.Body)

		assert.Contains(t, htmlDoc.Find(".timeline-item:has(.review-request-list)").Last().Text(), expectedText)
	}

	helper(t, "detach", "2", "refused to review")
	helper(t, "attach", "4", "requested reviews from user4 and removed review requests for user2")
	helper(t, "attach", "9", "requested reviews from user4, user9 and removed review requests for user2")
	helper(t, "attach", "2", "requested reviews from user4, user9")
	helper(t, "detach", "4", "requested review from user9")
	helper(t, "detach", "11", "requested reviews from user9 and removed review requests for user11")
	helper(t, "detach", "9", "removed review request for user11")
	helper(t, "detach", "2", "removed review requests for user11, user2")
}

func TestShowMergeForManualMerge(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	// Only allow manual merge strategy for this repository.
	pullRepoUnit := unittest.AssertExistsAndLoadBean(t, &repo_model.RepoUnit{ID: 5, RepoID: 1, Type: unit.TypePullRequests})
	pullRepoUnit.Config = &repo_model.PullRequestsConfig{
		AllowManualMerge:  true,
		DefaultMergeStyle: repo_model.MergeStyleManuallyMerged,
	}
	repo_model.UpdateRepoUnit(t.Context(), pullRepoUnit)

	session := loginUser(t, "user2")

	req := NewRequest(t, "GET", "/user2/repo1/pulls/5")
	resp := session.MakeRequest(t, req, http.StatusOK)

	// Assert that the mergebox is shown.
	htmlDoc := NewHTMLParser(t, resp.Body)
	htmlDoc.AssertElement(t, "#pull-request-merge-form", true)
}
