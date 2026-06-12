// Copyright 2023 The Gitea Authors. All rights reserved.
// Copyright 2024 The Forgejo Authors c/o Codeberg e.V.. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	auth_model "forgejo.org/models/auth"
	unit_model "forgejo.org/models/unit"
	"forgejo.org/modules/git"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/test"
	"forgejo.org/routers"
	"forgejo.org/services/release"
	"forgejo.org/tests"
	"forgejo.org/tests/forgery"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBadges(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		user := forgery.CreateUser(t, nil)

		assertBadge := func(t *testing.T, resp *httptest.ResponseRecorder, badge string) {
			t.Helper()

			assert.Equal(t, fmt.Sprintf("https://img.shields.io/badge/%s", badge), test.RedirectURL(resp))
		}

		t.Run("Workflows", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			// Actions disabled
			req := NewRequest(t, "GET", "/user2/repo1/badges/workflows/test.yaml/badge.svg")
			resp := MakeRequest(t, req, http.StatusSeeOther)
			assertBadge(t, resp, "test.yaml-Not%20found-crimson")

			req = NewRequest(t, "GET", "/user2/repo1/badges/workflows/test.yaml/badge.svg?branch=no-such-branch")
			resp = MakeRequest(t, req, http.StatusSeeOther)
			assertBadge(t, resp, "test.yaml-Not%20found-crimson")

			// Actions enabled
			repo := forgery.CreateRepository(t, user, &forgery.CreateRepositoryOptions{
				Files: forgery.MapFS{
					".gitea/workflows/pr.yml":         forgery.MapFile("name: pr\non:\n  push:\njobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo helloworld\n"),
					".gitea/workflows/self-test.yaml": forgery.MapFile("name: self-test\non:\n  push:\njobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo helloworld\n"),
					".gitea/workflows/tag-test.yaml":  forgery.MapFile("name: tags\non:\n  push:\n    tags: '*'\njobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo helloworld\n"),
				},
			})
			forgery.EnableRepoUnits(t, repo, unit_model.TypeActions)
			req = NewRequest(t, "GET", repo.HTMLURL()+"/badges/workflows/pr.yml/badge.svg")
			resp = MakeRequest(t, req, http.StatusSeeOther)
			assertBadge(t, resp, "pr.yml-waiting-lightgrey")

			req = NewRequest(t, "GET", repo.HTMLURL()+"/badges/workflows/pr.yml/badge.svg?branch=main")
			resp = MakeRequest(t, req, http.StatusSeeOther)
			assertBadge(t, resp, "pr.yml-waiting-lightgrey")

			req = NewRequest(t, "GET", repo.HTMLURL()+"/badges/workflows/pr.yml/badge.svg?branch=no-such-branch")
			resp = MakeRequest(t, req, http.StatusSeeOther)
			assertBadge(t, resp, "pr.yml-Not%20found-crimson")

			req = NewRequest(t, "GET", repo.HTMLURL()+"/badges/workflows/pr.yml/badge.svg?event=cron")
			resp = MakeRequest(t, req, http.StatusSeeOther)
			assertBadge(t, resp, "pr.yml-Not%20found-crimson")

			// Workflow with a dash in its name
			req = NewRequest(t, "GET", repo.HTMLURL()+"/badges/workflows/self-test.yaml/badge.svg")
			resp = MakeRequest(t, req, http.StatusSeeOther)
			assertBadge(t, resp, "self--test.yaml-waiting-lightgrey")

			// GitHub compatibility
			req = NewRequest(t, "GET", repo.HTMLURL()+"/actions/workflows/pr.yml/badge.svg")
			resp = MakeRequest(t, req, http.StatusSeeOther)
			assertBadge(t, resp, "pr.yml-waiting-lightgrey")

			req = NewRequest(t, "GET", repo.HTMLURL()+"/actions/workflows/pr.yml/badge.svg?branch=main")
			resp = MakeRequest(t, req, http.StatusSeeOther)
			assertBadge(t, resp, "pr.yml-waiting-lightgrey")

			req = NewRequest(t, "GET", repo.HTMLURL()+"/actions/workflows/pr.yml/badge.svg?branch=no-such-branch")
			resp = MakeRequest(t, req, http.StatusSeeOther)
			assertBadge(t, resp, "pr.yml-Not%20found-crimson")

			req = NewRequest(t, "GET", repo.HTMLURL()+"/actions/workflows/pr.yml/badge.svg?event=cron")
			resp = MakeRequest(t, req, http.StatusSeeOther)
			assertBadge(t, resp, "pr.yml-Not%20found-crimson")

			t.Run("tagged", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()

				// With no tags, the workflow has no runs, and isn't found
				req := NewRequest(t, "GET", repo.HTMLURL()+"/actions/workflows/tag-test.yaml/badge.svg")
				resp := MakeRequest(t, req, http.StatusSeeOther)
				assertBadge(t, resp, "tag--test.yaml-Not%20found-crimson")

				// Lets create a tag!
				err := release.CreateNewTag(git.DefaultContext, repo.Owner, repo, "main", "v1", "message")
				require.NoError(t, err)

				// Now the workflow is waiting
				req = NewRequest(t, "GET", repo.HTMLURL()+"/actions/workflows/tag-test.yaml/badge.svg")
				resp = MakeRequest(t, req, http.StatusSeeOther)
				assertBadge(t, resp, "tag--test.yaml-waiting-lightgrey")
			})
		})

		t.Run("Stars", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			repo := forgery.CreateRepository(t, user, nil)
			req := NewRequest(t, "GET", repo.HTMLURL()+"/badges/stars.svg")
			resp := MakeRequest(t, req, http.StatusSeeOther)

			assertBadge(t, resp, "stars-0-blue")

			t.Run("disabled stars", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()
				defer test.MockVariableValue(&setting.Repository.DisableStars, true)()
				defer test.MockVariableValue(&testWebRoutes, routers.NormalRoutes())()

				MakeRequest(t, req, http.StatusNotFound)
			})
		})

		t.Run("Issues", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			// Issues enabled
			req := NewRequest(t, "GET", "/user2/repo1/badges/issues.svg")
			resp := MakeRequest(t, req, http.StatusSeeOther)
			assertBadge(t, resp, "issues-2-blue")

			req = NewRequest(t, "GET", "/user2/repo1/badges/issues/open.svg")
			resp = MakeRequest(t, req, http.StatusSeeOther)
			assertBadge(t, resp, "issues-1%20open-blue")

			req = NewRequest(t, "GET", "/user2/repo1/badges/issues/closed.svg")
			resp = MakeRequest(t, req, http.StatusSeeOther)
			assertBadge(t, resp, "issues-1%20closed-blue")

			// Issues disabled
			repo := forgery.CreateRepository(t, user, nil)
			forgery.DisableRepoUnits(t, repo, unit_model.TypeIssues)
			req = NewRequest(t, "GET", repo.HTMLURL()+"/badges/issues.svg")
			resp = MakeRequest(t, req, http.StatusSeeOther)
			assertBadge(t, resp, "issues-Not%20found-crimson")

			req = NewRequest(t, "GET", repo.HTMLURL()+"/badges/issues/open.svg")
			resp = MakeRequest(t, req, http.StatusSeeOther)
			assertBadge(t, resp, "issues-Not%20found-crimson")

			req = NewRequest(t, "GET", repo.HTMLURL()+"/badges/issues/closed.svg")
			resp = MakeRequest(t, req, http.StatusSeeOther)
			assertBadge(t, resp, "issues-Not%20found-crimson")
		})

		t.Run("Pulls", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			// Pull requests enabled
			req := NewRequest(t, "GET", "/user2/repo1/badges/pulls.svg")
			resp := MakeRequest(t, req, http.StatusSeeOther)
			assertBadge(t, resp, "pulls-3-blue")

			req = NewRequest(t, "GET", "/user2/repo1/badges/pulls/open.svg")
			resp = MakeRequest(t, req, http.StatusSeeOther)
			assertBadge(t, resp, "pulls-3%20open-blue")

			req = NewRequest(t, "GET", "/user2/repo1/badges/pulls/closed.svg")
			resp = MakeRequest(t, req, http.StatusSeeOther)
			assertBadge(t, resp, "pulls-0%20closed-blue")

			// Pull requests disabled
			repo := forgery.CreateRepository(t, user, nil)
			forgery.DisableRepoUnits(t, repo, unit_model.TypePullRequests)
			req = NewRequest(t, "GET", repo.HTMLURL()+"/badges/pulls.svg")
			resp = MakeRequest(t, req, http.StatusSeeOther)
			assertBadge(t, resp, "pulls-Not%20found-crimson")

			req = NewRequest(t, "GET", repo.HTMLURL()+"/badges/pulls/open.svg")
			resp = MakeRequest(t, req, http.StatusSeeOther)
			assertBadge(t, resp, "pulls-Not%20found-crimson")

			req = NewRequest(t, "GET", repo.HTMLURL()+"/badges/pulls/closed.svg")
			resp = MakeRequest(t, req, http.StatusSeeOther)
			assertBadge(t, resp, "pulls-Not%20found-crimson")
		})

		t.Run("Release", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			req := NewRequest(t, "GET", "/user2/repo1/badges/release.svg")
			resp := MakeRequest(t, req, http.StatusSeeOther)
			assertBadge(t, resp, "release-v1.1-blue")

			repo := forgery.CreateRepository(t, user, &forgery.CreateRepositoryOptions{
				Files: forgery.FilesInit{}, // a tag will be made later
			})
			req = NewRequest(t, "GET", repo.HTMLURL()+"/badges/release.svg")
			resp = MakeRequest(t, req, http.StatusSeeOther)
			assertBadge(t, resp, "release-Not%20found-crimson")

			t.Run("Dashes in the name", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()

				session := loginUser(t, repo.Owner.Name)
				token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteRepository)
				err := release.CreateNewTag(git.DefaultContext, repo.Owner, repo, "main", "repo-name-2.0", "dash in the tag name")
				require.NoError(t, err)
				createNewReleaseUsingAPI(t, token, repo.Owner, repo, "repo-name-2.0", "main", "dashed release", "dashed release")

				req := NewRequest(t, "GET", repo.HTMLURL()+"/badges/release.svg")
				resp := MakeRequest(t, req, http.StatusSeeOther)
				assertBadge(t, resp, "release-repo--name--2.0-blue")
			})
		})
	})
}
