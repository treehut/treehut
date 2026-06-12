// Copyright 2021 The Gitea Authors. All rights reserved.
// Copyright 2024 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	auth_model "forgejo.org/models/auth"
	"forgejo.org/models/db"
	issues_model "forgejo.org/models/issues"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unit"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/structs"
	"forgejo.org/modules/test"
	"forgejo.org/modules/translation"
	"forgejo.org/services/migrations"
	"forgejo.org/tests"
	"forgejo.org/tests/forgery"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMigrateLocalPath(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	adminUser := unittest.AssertExistsAndLoadBean(t, &user_model.User{Name: "user1"})

	old := setting.ImportLocalPaths
	setting.ImportLocalPaths = true

	basePath := t.TempDir()

	lowercasePath := filepath.Join(basePath, "lowercase")
	err := os.Mkdir(lowercasePath, 0o700)
	require.NoError(t, err)

	err = migrations.IsMigrateURLAllowed(lowercasePath, adminUser)
	require.NoError(t, err, "case lowercase path")

	mixedcasePath := filepath.Join(basePath, "mIxeDCaSe")
	err = os.Mkdir(mixedcasePath, 0o700)
	require.NoError(t, err)

	err = migrations.IsMigrateURLAllowed(mixedcasePath, adminUser)
	require.NoError(t, err, "case mixedcase path")

	setting.ImportLocalPaths = old
}

func TestMigrate(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		defer test.MockVariableValue(&setting.Migrations.AllowLocalNetworks, true)()
		defer test.MockVariableValue(&setting.AppVer, "1.16.0")()
		require.NoError(t, migrations.Init())

		ownerName := "user2"
		repoName := "repo1"
		repoOwner := unittest.AssertExistsAndLoadBean(t, &user_model.User{Name: ownerName})
		session := loginUser(t, ownerName)
		token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteRepository, auth_model.AccessTokenScopeReadMisc)

		for _, s := range []struct {
			svc structs.GitServiceType
		}{
			{svc: structs.GiteaService},
			{svc: structs.ForgejoService},
		} {
			t.Run(s.svc.Name(), func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()
				// Step 0: verify the repo is available
				req := NewRequestf(t, "GET", "/%s/%s", ownerName, repoName)
				_ = session.MakeRequest(t, req, http.StatusOK)
				// Step 1: get the Gitea migration form
				req = NewRequestf(t, "GET", "/repo/migrate/?service_type=%d", s.svc)
				resp := session.MakeRequest(t, req, http.StatusOK)
				// Step 2: load the form
				htmlDoc := NewHTMLParser(t, resp.Body)
				// Check form title
				title := htmlDoc.doc.Find("title").Text()
				assert.Contains(t, title, translation.NewLocale("en-US").TrString("new_migrate.title"))
				// Get the link of migration button
				link, exists := htmlDoc.doc.Find(`form.ui.form[action^="/repo/migrate"]`).Attr("action")
				assert.True(t, exists, "The template has changed")
				// Step 4: submit the migration to only migrate issues
				migratedRepoName := "otherrepo-" + s.svc.Name()
				req = NewRequestWithValues(t, "POST", link, map[string]string{
					"service":     fmt.Sprintf("%d", s.svc),
					"clone_addr":  fmt.Sprintf("%s%s/%s", u, ownerName, repoName),
					"auth_token":  token,
					"issues":      "on",
					"repo_name":   migratedRepoName,
					"description": "",
					"uid":         fmt.Sprintf("%d", repoOwner.ID),
				})
				resp = session.MakeRequest(t, req, http.StatusSeeOther)
				// Step 5: a redirection displays the migrated repository
				assert.Equal(t, fmt.Sprintf("/%s/%s", ownerName, migratedRepoName), test.RedirectURL(resp))
				// Step 6: check the repo was created
				unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{Name: migratedRepoName})
			})
		}
	})
}

func TestMigrateWithIssueComments(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		defer test.MockVariableValue(&setting.Migrations.AllowLocalNetworks, true)()
		defer test.MockVariableValue(&setting.AppVer, "1.16.0")()
		maxResponseItems := 10
		numComments := 21
		defer test.MockVariableValue(&setting.API.MaxResponseItems, maxResponseItems)()

		require.NoError(t, migrations.Init())

		ownerName := "user5"
		repoName := "repo4"
		repoOwner := unittest.AssertExistsAndLoadBean(t, &user_model.User{Name: ownerName})
		session := loginUser(t, ownerName)
		token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteRepository, auth_model.AccessTokenScopeReadMisc, auth_model.AccessTokenScopeAll)

		// Add issue to repo
		issueURL := testNewIssue(t, session, ownerName, repoName, "testIssue", "testIssue")
		t.Logf("issueUrl %v", issueURL)

		// Add comments to issue
		for i := 1; i <= numComments; i++ {
			testIssueAddComment(t, session, issueURL, strconv.Itoa(i), "")
		}

		repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{OwnerID: repoOwner.ID, Name: repoName})
		assert.Equal(t, 1, repo.NumIssues(t.Context()))

		issue := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{RepoID: repo.ID, Index: 1})
		assert.Equal(t, numComments, issue.NumComments)

		for _, s := range []struct {
			svc structs.GitServiceType
		}{
			{svc: structs.GiteaService},
			{svc: structs.ForgejoService},
		} {
			t.Run(s.svc.Name(), func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()
				// Step 0: verify the repo is available
				req := NewRequestf(t, "GET", "/%s/%s", ownerName, repoName)
				_ = session.MakeRequest(t, req, http.StatusOK)
				// Step 1: get the Gitea migration form
				req = NewRequestf(t, "GET", "/repo/migrate/?service_type=%d", s.svc)
				resp := session.MakeRequest(t, req, http.StatusOK)
				// Step 2: load the form
				htmlDoc := NewHTMLParser(t, resp.Body)
				// Check form title
				title := htmlDoc.doc.Find("title").Text()
				assert.Contains(t, title, translation.NewLocale("en-US").TrString("new_migrate.title"))
				// Get the link of migration button
				link, exists := htmlDoc.doc.Find(`form.ui.form[action^="/repo/migrate"]`).Attr("action")
				assert.True(t, exists, "The template has changed")
				// Step 4: submit the migration to only migrate issues
				migratedRepoName := "otherrepo-" + s.svc.Name()
				req = NewRequestWithValues(t, "POST", link, map[string]string{
					"service":     fmt.Sprintf("%d", s.svc),
					"clone_addr":  fmt.Sprintf("%s%s/%s", u, ownerName, repoName),
					"auth_token":  token,
					"issues":      "on",
					"repo_name":   migratedRepoName,
					"description": "",
					"uid":         fmt.Sprintf("%d", repoOwner.ID),
				})
				resp = session.MakeRequest(t, req, http.StatusSeeOther)
				// Step 5: a redirection displays the migrated repository
				assert.Equal(t, fmt.Sprintf("/%s/%s", ownerName, migratedRepoName), test.RedirectURL(resp))
				// Step 6: check the repo was created
				migratedRepo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{Name: migratedRepoName})
				issue := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{RepoID: migratedRepo.ID, Index: 1})
				assert.Equal(t, numComments, issue.NumComments)
			})
		}
	})
}

func TestMigrateWithWiki(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		defer test.MockVariableValue(&setting.Migrations.AllowLocalNetworks, true)()
		defer test.MockVariableValue(&setting.AppVer, "1.16.0")()
		require.NoError(t, migrations.Init())

		repo := forgery.CreateRepository(t, nil, nil)
		forgery.InitWiki(t, repo, "obscure-name")
		user := repo.Owner

		session := loginUser(t, user.Name)
		token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteRepository, auth_model.AccessTokenScopeReadMisc)

		for _, s := range []struct {
			svc structs.GitServiceType
		}{
			{svc: structs.GiteaService},
			{svc: structs.ForgejoService},
		} {
			t.Run(s.svc.Name(), func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()
				// Step 0: verify the repo is available
				req := NewRequestf(t, "GET", "/%s", repo.FullName())
				_ = session.MakeRequest(t, req, http.StatusOK)
				// Step 1: get the Gitea migration form
				req = NewRequestf(t, "GET", "/repo/migrate/?service_type=%d", s.svc)
				resp := session.MakeRequest(t, req, http.StatusOK)
				// Step 2: load the form
				htmlDoc := NewHTMLParser(t, resp.Body)
				// Check form title
				title := htmlDoc.doc.Find("title").Text()
				assert.Contains(t, title, translation.NewLocale("en-US").TrString("new_migrate.title"))
				// Step 4: submit the migration to only migrate issues
				migratedRepoName := "otherrepo-" + s.svc.Name()
				req = NewRequestWithValues(t, "POST", "/repo/migrate", map[string]string{
					"service":     fmt.Sprintf("%d", s.svc),
					"clone_addr":  fmt.Sprintf("%s%s", u, repo.FullName()),
					"auth_token":  token,
					"issues":      "on",
					"wiki":        "on",
					"repo_name":   migratedRepoName,
					"description": "",
					"uid":         fmt.Sprintf("%d", user.ID),
				})
				resp = session.MakeRequest(t, req, http.StatusSeeOther)
				// Step 5: a redirection displays the migrated repository
				assert.Equal(t, fmt.Sprintf("/%s/%s", user.Name, migratedRepoName), test.RedirectURL(resp))
				// Step 6: check the repo was created and load the repo
				migratedRepo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{Name: migratedRepoName, WikiBranch: "obscure-name"})
				// Step 7: check if the wiki is enabled
				assert.True(t, migratedRepo.UnitEnabled(db.DefaultContext, unit.TypeWiki))
			})
		}
	})
}

func TestMigrateWithReleases(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		defer test.MockVariableValue(&setting.Migrations.AllowLocalNetworks, true)()
		defer test.MockVariableValue(&setting.AppVer, "1.16.0")()
		require.NoError(t, migrations.Init())

		ownerName := "user2"
		repoName := "repo1"
		repoOwner := unittest.AssertExistsAndLoadBean(t, &user_model.User{Name: ownerName})
		session := loginUser(t, ownerName)
		token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteRepository, auth_model.AccessTokenScopeReadMisc)

		for _, s := range []struct {
			svc structs.GitServiceType
		}{
			{svc: structs.GiteaService},
			{svc: structs.ForgejoService},
		} {
			t.Run(s.svc.Name(), func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()
				// Step 0: verify the repo is available
				req := NewRequestf(t, "GET", "/%s/%s", ownerName, repoName)
				_ = session.MakeRequest(t, req, http.StatusOK)
				// Step 1: get the Gitea migration form
				req = NewRequestf(t, "GET", "/repo/migrate/?service_type=%d", s.svc)
				resp := session.MakeRequest(t, req, http.StatusOK)
				// Step 2: load the form
				htmlDoc := NewHTMLParser(t, resp.Body)
				// Check form title
				title := htmlDoc.doc.Find("title").Text()
				assert.Contains(t, title, translation.NewLocale("en-US").TrString("new_migrate.title"))
				// Step 4: submit the migration to only migrate issues
				migratedRepoName := "otherrepo-" + s.svc.Name()
				req = NewRequestWithValues(t, "POST", "/repo/migrate", map[string]string{
					"service":     fmt.Sprintf("%d", s.svc),
					"clone_addr":  fmt.Sprintf("%s%s/%s", u, ownerName, repoName),
					"auth_token":  token,
					"issues":      "on",
					"releases":    "on",
					"repo_name":   migratedRepoName,
					"description": "",
					"uid":         fmt.Sprintf("%d", repoOwner.ID),
				})
				resp = session.MakeRequest(t, req, http.StatusSeeOther)
				// Step 5: a redirection displays the migrated repository
				assert.Equal(t, fmt.Sprintf("/%s/%s", ownerName, migratedRepoName), test.RedirectURL(resp))
				// Step 6: check the repo was created and load the repo
				migratedRepo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{Name: migratedRepoName})
				// Step 7: check if releases are enabled
				assert.True(t, migratedRepo.UnitEnabled(db.DefaultContext, unit.TypeReleases))
			})
		}
	})
}

func Test_UpdateCommentsMigrationsByType(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	err := issues_model.UpdateCommentsMigrationsByType(db.DefaultContext, structs.GithubService, "1", 1)
	require.NoError(t, err)
}
