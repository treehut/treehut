// Copyright 2017 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"net/http"
	"net/url"
	"testing"

	"forgejo.org/models/db"
	repo_model "forgejo.org/models/repo"
	code_indexer "forgejo.org/modules/indexer/code"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/test"
	"forgejo.org/routers"
	"forgejo.org/tests"

	"github.com/PuerkitoBio/goquery"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func resultFilenames(t testing.TB, doc *HTMLDoc) []string {
	resultSelections := doc.
		Find(".repository.search").
		Find("details.repo-search-result")

	result := make([]string, resultSelections.Length())
	resultSelections.Each(func(i int, selection *goquery.Selection) {
		assert.Positive(t, selection.Find("div ol li").Length(), 0)
		assert.Positive(t, selection.Find(".code-inner").Find(".search-highlight").Length(), 0)
		result[i] = selection.
			Find(".header").
			Find("span.file a.file-link").
			First().
			Text()
	})

	return result
}

func TestSearchRepoIndexer(t *testing.T) {
	testSearchRepo(t, true)
}

func TestSearchRepoNoIndexer(t *testing.T) {
	testSearchRepo(t, false)
}

func testSearchRepo(t *testing.T, indexer bool) {
	defer tests.PrepareTestEnv(t)()
	defer test.MockVariableValue(&setting.Indexer.RepoIndexerEnabled, indexer)()
	defer test.MockVariableValue(&testWebRoutes, routers.NormalRoutes())()

	repo, err := repo_model.GetRepositoryByOwnerAndName(db.DefaultContext, "user2", "repo1")
	require.NoError(t, err)

	if indexer {
		code_indexer.UpdateRepoIndexer(repo)
	}

	testSearch(t, "/user2/repo1/search?q=Description&page=1", []string{"README.md"}, indexer)

	req := NewRequest(t, "HEAD", "/user2/repo1/search/branch/"+repo.DefaultBranch)
	if indexer {
		MakeRequest(t, req, http.StatusNotFound)
	} else {
		MakeRequest(t, req, http.StatusOK)
	}

	defer test.MockVariableValue(&setting.Indexer.IncludePatterns, setting.IndexerGlobFromString("**.txt"))()
	defer test.MockVariableValue(&setting.Indexer.ExcludePatterns, setting.IndexerGlobFromString("**/y/**"))()

	repo, err = repo_model.GetRepositoryByOwnerAndName(db.DefaultContext, "user2", "glob")
	require.NoError(t, err)

	if indexer {
		code_indexer.UpdateRepoIndexer(repo)
	}

	testSearch(t, "/user2/glob/search?q=", []string{}, indexer)
	testSearch(t, "/user2/glob/search?q=loren&page=1", []string{"a.txt"}, indexer)
	testSearch(t, "/user2/glob/search?q=loren&page=1&mode=exact", []string{"a.txt"}, indexer)

	// union search: Union/OR of all the keywords
	testSearch(t, "/user2/glob/search?q=file3+file1&mode=union&page=1", []string{"a.txt", "x/b.txt"}, indexer)
	testSearch(t, "/user2/glob/search?q=file4&mode=union&page=1", []string{}, indexer)
	testSearch(t, "/user2/glob/search?q=file5&mode=union&page=1", []string{}, indexer)

	testSearch(t, "/user2/glob/search?q=file3&page=1&mode=exact", []string{"x/b.txt"}, indexer)
	testSearch(t, "/user2/glob/search?q=file4&page=1&mode=exact", []string{}, indexer)
	testSearch(t, "/user2/glob/search?q=file5&page=1&mode=exact", []string{}, indexer)
}

func testSearch(t *testing.T, rawURL string, expected []string, indexer bool) {
	req := NewRequest(t, "GET", rawURL)
	resp := MakeRequest(t, req, http.StatusOK)

	doc := NewHTMLParser(t, resp.Body)
	container := doc.Find(".repository").Find(".ui.container")

	branchDropdown := container.Find(".js-branch-tag-selector")
	assert.Equal(t, indexer, len(branchDropdown.Nodes) == 0)

	dropdownOptions := container.
		Find(".menu[data-test-tag=fuzzy-dropdown]").
		Find("input[type=radio][name=mode]").
		Map(func(_ int, sel *goquery.Selection) string {
			attr, exists := sel.Attr("value")
			assert.True(t, exists)
			return attr
		})

	if indexer {
		assert.Equal(t, []string{"exact", "union"}, dropdownOptions)
	} else {
		assert.Equal(t, []string{"exact", "union", "regexp"}, dropdownOptions)
	}

	filenames := resultFilenames(t, doc)
	assert.ElementsMatch(t, expected, filenames)

	testSearchPagination(t, rawURL, doc)
}

// Tests that the variables set in the url persist for all the paginated links
func testSearchPagination(t *testing.T, rawURL string, doc *HTMLDoc) {
	original, err := queryFromStr(rawURL)
	require.NoError(t, err)

	hrefs := doc.
		Find(".pagination.menu a[href]:not(.disabled)").
		Map(func(i int, el *goquery.Selection) string {
			attr, ok := el.Attr("href")
			require.True(t, ok)
			return attr
		})
	query := make([]url.Values, len(hrefs))
	for i, href := range hrefs {
		query[i], err = queryFromStr(href)
		require.NoError(t, err)
	}

	for key := range original {
		for i, q := range query {
			assert.Equal(t, original.Get(key), q.Get(key),
				"failed at index '%d' with url '%v'", i, hrefs[i])
		}
	}
}

func queryFromStr(rawURL string) (url.Values, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}

	return url.ParseQuery(u.RawQuery)
}
