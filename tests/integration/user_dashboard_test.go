// Copyright 2024-2025 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package integration

import (
	"net/http"
	"net/url"
	"strconv"
	"testing"

	"forgejo.org/models/issues"
	"forgejo.org/models/unittest"
	"forgejo.org/modules/gitrepo"
	issue_service "forgejo.org/services/issue"
	pull_service "forgejo.org/services/pull"
	"forgejo.org/tests/forgery"

	"github.com/PuerkitoBio/goquery"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUserDashboardFeedWelcome(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	// User2 has some activity in feed
	session := loginUser(t, "user2")
	page := NewHTMLParser(t, session.MakeRequest(t, NewRequest(t, "GET", "/"), http.StatusOK).Body)
	testUserDashboardFeedType(t, page, false)

	// User1 doesn't have any activity in feed
	session = loginUser(t, "user1")
	page = NewHTMLParser(t, session.MakeRequest(t, NewRequest(t, "GET", "/"), http.StatusOK).Body)
	testUserDashboardFeedType(t, page, true)
}

func testUserDashboardFeedType(t *testing.T, page *HTMLDoc, isEmpty bool) {
	page.AssertElement(t, "#activity-feed", !isEmpty)
	page.AssertElement(t, "#empty-feed", isEmpty)
}

func TestDashboardTitleRendering(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		user := forgery.CreateUser(t, nil)
		sess := loginUser(t, user.Name)

		repo := forgery.CreateRepository(t, user, &forgery.CreateRepositoryOptions{
			Files: forgery.MapFS{
				"README.md": forgery.MapFile("some readme to update via the pull request"),
				"test.txt":  forgery.MapFile("Just some text here"),
			},
		})

		issue := createIssue(t, user, repo, "`:exclamation:` not rendered", "Hi there!")
		pr := createPullRequest(t, user, repo, "testing", "`:exclamation:` not rendered")

		_, err := issue_service.CreateIssueComment(t.Context(), user, repo, issue, "hi", nil)
		require.NoError(t, err)

		_, err = issue_service.CreateIssueComment(t.Context(), user, repo, pr.Issue, "hi", nil)
		require.NoError(t, err)

		testIssueClose(t, sess, repo.OwnerName, repo.Name, strconv.Itoa(int(issue.Index)), false)
		testIssueClose(t, sess, repo.OwnerName, repo.Name, strconv.Itoa(int(pr.Issue.Index)), true)

		response := sess.MakeRequest(t, NewRequest(t, "GET", "/"), http.StatusOK)
		htmlDoc := NewHTMLParser(t, response.Body)

		count := 0
		htmlDoc.doc.Find("#activity-feed .flex-item-main .title").Each(func(i int, s *goquery.Selection) {
			count++
			if s.IsMatcher(goquery.Single("a")) {
				assert.Equal(t, "❗ not rendered", s.Text())
			} else {
				assert.Equal(t, ":exclamation: not rendered", s.Text())
			}
		})

		assert.Equal(t, 6, count)
	})
}

func TestDashboardActionEscaping(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		user := forgery.CreateUser(t, nil)
		sess := loginUser(t, user.Name)

		repo := forgery.CreateRepository(t, user, &forgery.CreateRepositoryOptions{
			Files: forgery.FilesInit{},
		})

		issue := createIssue(t, user, repo, "Issue with | in title", "Hey here's a | for you")

		_, err := issue_service.CreateIssueComment(t.Context(), user, repo, issue, "Comment with a | in it", nil)
		require.NoError(t, err)

		testIssueClose(t, sess, repo.OwnerName, repo.Name, strconv.Itoa(int(issue.Index)), false)

		response := sess.MakeRequest(t, NewRequest(t, "GET", "/"), http.StatusOK)
		htmlDoc := NewHTMLParser(t, response.Body)

		count := 0
		htmlDoc.doc.Find("#activity-feed .flex-item-main .title").Each(func(i int, s *goquery.Selection) {
			count++
			assert.Equal(t, "Issue with | in title", s.Text())
		})
		htmlDoc.doc.Find("#activity-feed .flex-item-main .markup").Each(func(i int, s *goquery.Selection) {
			count++
			assert.Equal(t, "Comment with a | in it\n", s.Text())
		})

		assert.Equal(t, 4, count)
	})
}

func TestDashboardReviewWorkflows(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		user := forgery.CreateUser(t, nil)
		sess := loginUser(t, user.Name)

		repo := forgery.CreateRepository(t, user, &forgery.CreateRepositoryOptions{
			Files: forgery.FilesInit{},
		})

		gitRepo, err := gitrepo.OpenRepository(t.Context(), repo)
		require.NoError(t, err)

		pr := createPullRequest(t, user, repo, "testing", "My very first PR!")

		review, _, err := pull_service.SubmitReview(t.Context(), user, gitRepo, pr.Issue, issues.ReviewTypeReject, "This isn't good enough!", "HEAD", []string{})
		require.NoError(t, err)

		_, err = pull_service.DismissReview(t.Context(), review.ID, repo.ID, "Come on, give the newbie a break!", user, true, true)
		require.NoError(t, err)

		response := sess.MakeRequest(t, NewRequest(t, "GET", "/"), http.StatusOK)
		htmlDoc := NewHTMLParser(t, response.Body)

		count := 0
		htmlDoc.doc.Find("#activity-feed .flex-item-main .title").Each(func(i int, s *goquery.Selection) {
			count++
			assert.Equal(t, "My very first PR!", s.Text())
		})
		htmlDoc.doc.Find("#activity-feed .flex-item-main .flex-item-body").Each(func(i int, s *goquery.Selection) {
			count++
			if s.Text() != "Reason:" && s.Text() != "Come on, give the newbie a break!" {
				assert.Fail(t, "Unexpected feed text", "Expected 'Reason:' and reason explanation, but found: %q", s.Text())
			}
		})

		assert.Equal(t, 4, count)
	})
}
