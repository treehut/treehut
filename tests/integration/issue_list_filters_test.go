// Copyright 2024 The Forgejo Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package integration

import (
	"net/http"
	"net/url"
	"testing"

	"forgejo.org/modules/translation"
	"forgejo.org/tests"

	"github.com/PuerkitoBio/goquery"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Tests for contents of pages .../issues and .../pulls

func TestIssueFilterLabels(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	t.Run("Exclusion tooltips", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		url := "/user2/repo1/issues?labels=-2"
		page := NewHTMLParser(t, MakeRequest(t, NewRequest(t, "GET", url), http.StatusOK).Body)

		page.AssertElement(t, ".label-filter .menu .item:has(a[data-label-id='1']) button[data-tooltip-content='Exclude label']", true)
		page.AssertElement(t, ".label-filter .menu .item:has(a[data-label-id='2']) button[data-tooltip-content='Clear exclusion']", true)
	})
}

func TestIssueSorting(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	t.Run("Dropdown content", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		req := NewRequest(t, "GET", "/user2/repo1/issues")
		resp := MakeRequest(t, req, http.StatusOK)
		htmlDoc := NewHTMLParser(t, resp.Body)

		assert.Equal(t,
			9,
			htmlDoc.Find(`.list-header-sort .menu a`).Length(),
			"Wrong amount of sort options in dropdown")

		locale := translation.NewLocale("en-US")
		keys := []string{
			"relevance",
			"latest",
			"oldest",
			"recentupdate",
			"leastupdate",
			"mostcomment",
			"leastcomment",
			"nearduedate",
			"farduedate",
		}

		actual := htmlDoc.
			Find(`.list-header-sort .menu .item`).
			Map(func(i int, element *goquery.Selection) string {
				href, ok := element.Attr("href")
				assert.True(t, ok)

				values, err := url.ParseQuery(href)
				require.NoError(t, err)
				assert.True(t, values.Has("sort"))

				sort := values.Get("sort")
				assert.Contains(t, keys, sort)
				assert.Contains(t, element.Text(), locale.Tr("repo.issues.filter_sort."+sort))
				return sort
			})

		assert.Equal(t, keys, actual)
	})
}

func TestIssueFilterLinks(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	t.Run("No filters", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		req := NewRequest(t, "GET", "/user2/repo1/issues")
		resp := MakeRequest(t, req, http.StatusOK)
		htmlDoc := NewHTMLParser(t, resp.Body)

		called := false
		htmlDoc.Find("#issue-filters a[href^='?']").Each(func(_ int, s *goquery.Selection) {
			called = true
			href, _ := s.Attr("href")
			assert.Contains(t, href, "?q=&")
			assert.Contains(t, href, "&type=")
			assert.Contains(t, href, "&sort=")
			assert.Contains(t, href, "&state=")
			assert.Contains(t, href, "&labels=")
			assert.Contains(t, href, "&milestone=")
			assert.Contains(t, href, "&project=")
			assert.Contains(t, href, "&assignee=")
			assert.Contains(t, href, "&poster=")
		})
		assert.True(t, called)
	})

	t.Run("Keyword", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		req := NewRequest(t, "GET", "/user2/repo1/issues?q=search-on-this")
		resp := MakeRequest(t, req, http.StatusOK)
		htmlDoc := NewHTMLParser(t, resp.Body)

		called := false
		htmlDoc.Find("#issue-filters a[href^='?']").Each(func(_ int, s *goquery.Selection) {
			called = true
			href, _ := s.Attr("href")
			assert.Contains(t, href, "?q=search-on-this")
			assert.Contains(t, href, "&type=")
			assert.Contains(t, href, "&sort=")
			assert.Contains(t, href, "&state=")
			assert.Contains(t, href, "&labels=")
			assert.Contains(t, href, "&milestone=")
			assert.Contains(t, href, "&project=")
			assert.Contains(t, href, "&assignee=")
			assert.Contains(t, href, "&poster=")
		})
		assert.True(t, called)
	})

	t.Run("Sort", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		req := NewRequest(t, "GET", "/user2/repo1/issues?sort=oldest")
		resp := MakeRequest(t, req, http.StatusOK)
		htmlDoc := NewHTMLParser(t, resp.Body)

		called := false
		htmlDoc.Find("#issue-filters a[href^='?']:not(.list-header-sort a)").Each(func(_ int, s *goquery.Selection) {
			called = true
			href, _ := s.Attr("href")
			assert.Contains(t, href, "?q=&")
			assert.Contains(t, href, "&type=")
			assert.Contains(t, href, "&sort=oldest")
			assert.Contains(t, href, "&state=")
			assert.Contains(t, href, "&labels=")
			assert.Contains(t, href, "&milestone=")
			assert.Contains(t, href, "&project=")
			assert.Contains(t, href, "&assignee=")
			assert.Contains(t, href, "&poster=")
		})
		assert.True(t, called)
	})

	t.Run("Type", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		req := NewRequest(t, "GET", "/user2/repo1/issues?type=assigned")
		resp := MakeRequest(t, req, http.StatusOK)
		htmlDoc := NewHTMLParser(t, resp.Body)

		called := false
		htmlDoc.Find("#issue-filters a[href^='?']:not(.list-header-type a)").Each(func(_ int, s *goquery.Selection) {
			called = true
			href, _ := s.Attr("href")
			assert.Contains(t, href, "?q=&")
			assert.Contains(t, href, "&type=assigned")
			assert.Contains(t, href, "&sort=")
			assert.Contains(t, href, "&state=")
			assert.Contains(t, href, "&labels=")
			assert.Contains(t, href, "&milestone=")
			assert.Contains(t, href, "&project=")
			assert.Contains(t, href, "&assignee=")
			assert.Contains(t, href, "&poster=")
		})
		assert.True(t, called)
	})

	t.Run("State", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		req := NewRequest(t, "GET", "/user2/repo1/issues?state=closed")
		resp := MakeRequest(t, req, http.StatusOK)
		htmlDoc := NewHTMLParser(t, resp.Body)

		called := false
		htmlDoc.Find("#issue-filters a[href^='?']:not(.issue-list-toolbar-left a)").Each(func(_ int, s *goquery.Selection) {
			called = true
			href, _ := s.Attr("href")
			assert.Contains(t, href, "?q=&")
			assert.Contains(t, href, "&type=")
			assert.Contains(t, href, "&sort=")
			assert.Contains(t, href, "&state=closed")
			assert.Contains(t, href, "&labels=")
			assert.Contains(t, href, "&milestone=")
			assert.Contains(t, href, "&project=")
			assert.Contains(t, href, "&assignee=")
			assert.Contains(t, href, "&poster=")
		})
		assert.True(t, called)
	})

	t.Run("Milestone", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		req := NewRequest(t, "GET", "/user2/repo1/issues?milestone=1")
		resp := MakeRequest(t, req, http.StatusOK)
		htmlDoc := NewHTMLParser(t, resp.Body)

		called := false
		htmlDoc.Find("#issue-filters a[href^='?']:not(.list-header-milestone a)").Each(func(_ int, s *goquery.Selection) {
			called = true
			href, _ := s.Attr("href")
			assert.Contains(t, href, "?q=&")
			assert.Contains(t, href, "&type=")
			assert.Contains(t, href, "&sort=")
			assert.Contains(t, href, "&state=")
			assert.Contains(t, href, "&labels=")
			assert.Contains(t, href, "&milestone=1")
			assert.Contains(t, href, "&project=")
			assert.Contains(t, href, "&assignee=")
			assert.Contains(t, href, "&poster=")
		})
		assert.True(t, called)
	})

	t.Run("Milestone", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		req := NewRequest(t, "GET", "/user2/repo1/issues?milestone=1")
		resp := MakeRequest(t, req, http.StatusOK)
		htmlDoc := NewHTMLParser(t, resp.Body)

		called := false
		htmlDoc.Find("#issue-filters a[href^='?']:not(.list-header-milestone a)").Each(func(_ int, s *goquery.Selection) {
			called = true
			href, _ := s.Attr("href")
			assert.Contains(t, href, "?q=&")
			assert.Contains(t, href, "&type=")
			assert.Contains(t, href, "&sort=")
			assert.Contains(t, href, "&state=")
			assert.Contains(t, href, "&labels=")
			assert.Contains(t, href, "&milestone=1")
			assert.Contains(t, href, "&project=")
			assert.Contains(t, href, "&assignee=")
			assert.Contains(t, href, "&poster=")
		})
		assert.True(t, called)
	})

	t.Run("Project", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		req := NewRequest(t, "GET", "/user2/repo1/issues?project=1")
		resp := MakeRequest(t, req, http.StatusOK)
		htmlDoc := NewHTMLParser(t, resp.Body)

		called := false
		htmlDoc.Find("#issue-filters a[href^='?']:not(.list-header-project a)").Each(func(_ int, s *goquery.Selection) {
			called = true
			href, _ := s.Attr("href")
			assert.Contains(t, href, "?q=&")
			assert.Contains(t, href, "&type=")
			assert.Contains(t, href, "&sort=")
			assert.Contains(t, href, "&state=")
			assert.Contains(t, href, "&labels=")
			assert.Contains(t, href, "&milestone=")
			assert.Contains(t, href, "&project=1")
			assert.Contains(t, href, "&assignee=")
			assert.Contains(t, href, "&poster=")
		})
		assert.True(t, called)
	})

	t.Run("Assignee", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		req := NewRequest(t, "GET", "/user2/repo1/issues?assignee=1")
		resp := MakeRequest(t, req, http.StatusOK)
		htmlDoc := NewHTMLParser(t, resp.Body)

		called := false
		htmlDoc.Find("#issue-filters a[href^='?']:not(.list-header-assignee a)").Each(func(_ int, s *goquery.Selection) {
			called = true
			href, _ := s.Attr("href")
			assert.Contains(t, href, "?q=&")
			assert.Contains(t, href, "&type=")
			assert.Contains(t, href, "&sort=")
			assert.Contains(t, href, "&state=")
			assert.Contains(t, href, "&labels=")
			assert.Contains(t, href, "&milestone=")
			assert.Contains(t, href, "&project=")
			assert.Contains(t, href, "&assignee=1")
			assert.Contains(t, href, "&poster=")
		})
		assert.True(t, called)
	})

	t.Run("Poster", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		req := NewRequest(t, "GET", "/user2/repo1/issues?poster=1")
		resp := MakeRequest(t, req, http.StatusOK)
		htmlDoc := NewHTMLParser(t, resp.Body)

		called := false
		htmlDoc.Find("#issue-filters a[href^='?']:not(.list-header-poster a)").Each(func(_ int, s *goquery.Selection) {
			called = true
			href, _ := s.Attr("href")
			assert.Contains(t, href, "?q=&")
			assert.Contains(t, href, "&type=")
			assert.Contains(t, href, "&sort=")
			assert.Contains(t, href, "&state=")
			assert.Contains(t, href, "&labels=")
			assert.Contains(t, href, "&milestone=")
			assert.Contains(t, href, "&project=")
			assert.Contains(t, href, "&assignee=")
			assert.Contains(t, href, "&poster=1")
		})
		assert.True(t, called)
	})

	t.Run("Labels", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		req := NewRequest(t, "GET", "/user2/repo1/issues?labels=1")
		resp := MakeRequest(t, req, http.StatusOK)
		htmlDoc := NewHTMLParser(t, resp.Body)

		called := false
		htmlDoc.Find("#issue-filters a[href^='?']:not(.label-filter a)").Each(func(_ int, s *goquery.Selection) {
			called = true
			href, _ := s.Attr("href")
			assert.Contains(t, href, "?q=&")
			assert.Contains(t, href, "&type=")
			assert.Contains(t, href, "&sort=")
			assert.Contains(t, href, "&state=")
			assert.Contains(t, href, "&labels=1")
			assert.Contains(t, href, "&milestone=")
			assert.Contains(t, href, "&project=")
			assert.Contains(t, href, "&assignee=")
			assert.Contains(t, href, "&poster=")
		})
		assert.True(t, called)
	})

	t.Run("Archived labels", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		req := NewRequest(t, "GET", "/user2/repo1/issues?archived=true")
		resp := MakeRequest(t, req, http.StatusOK)
		htmlDoc := NewHTMLParser(t, resp.Body)

		called := false
		htmlDoc.Find("#issue-filters a[href^='?']").Each(func(_ int, s *goquery.Selection) {
			called = true
			href, _ := s.Attr("href")
			assert.Contains(t, href, "?q=&")
			assert.Contains(t, href, "&type=")
			assert.Contains(t, href, "&sort=")
			assert.Contains(t, href, "&state=")
			assert.Contains(t, href, "&labels=")
			assert.Contains(t, href, "&milestone=")
			assert.Contains(t, href, "&project=")
			assert.Contains(t, href, "&assignee=")
			assert.Contains(t, href, "&poster=")
			assert.Contains(t, href, "&archived=true")
		})
		assert.True(t, called)
	})
}
