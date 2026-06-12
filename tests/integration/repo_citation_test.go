// Copyright 2024 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package integration

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	repo_model "forgejo.org/models/repo"
	"forgejo.org/tests"
	"forgejo.org/tests/forgery"

	"github.com/stretchr/testify/assert"
)

func TestCitation(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		user := forgery.CreateUser(t, nil)

		t.Run("No citation", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			repo := forgery.CreateRepository(t, user, &forgery.CreateRepositoryOptions{
				Files: forgery.MapFS{
					"README": forgery.MapFile("no citation file"),
				},
			})

			testCitationButtonExists(t, repo, "")
		})

		t.Run("cff citation", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			repo := forgery.CreateRepository(t, user, &forgery.CreateRepositoryOptions{
				Files: forgery.MapFS{
					"CITATION.cff": forgery.MapFile("some content"),
				},
			})

			testCitationButtonExists(t, repo, "CITATION.cff")
		})

		t.Run("bib citation", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			repo := forgery.CreateRepository(t, user, &forgery.CreateRepositoryOptions{
				Files: forgery.MapFS{
					"CITATION.bib": forgery.MapFile("some content"),
				},
			})

			testCitationButtonExists(t, repo, "CITATION.bib")
		})
	})
}

func testCitationButtonExists(t *testing.T, repo *repo_model.Repository, file string) {
	req := NewRequest(t, "GET", repo.HTMLURL())
	resp := MakeRequest(t, req, http.StatusOK)
	doc := NewHTMLParser(t, resp.Body)

	links := doc.Find("a.citation-link")
	if file == "" {
		assert.Equal(t, 0, links.Length())
		return
	}

	assert.Equal(t, 1, links.Length())
	href, exists := links.Attr("href")
	assert.True(t, exists)
	assert.True(t, strings.HasSuffix(href, file))

	// request the citation file to check for webcomponent presence
	req = NewRequest(t, "GET", href)
	resp = MakeRequest(t, req, http.StatusOK)
	doc = NewHTMLParser(t, resp.Body)
	doc.AssertElement(t, `lazy-webc[tag="citation-information"]`, true)
}
