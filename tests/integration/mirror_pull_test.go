// Copyright 2019 The Gitea Authors. All rights reserved.
// Copyright 2025 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"testing"
	"time"

	"forgejo.org/models/auth"
	"forgejo.org/models/db"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/git"
	"forgejo.org/modules/gitrepo"
	"forgejo.org/modules/lfs"
	"forgejo.org/modules/log"
	"forgejo.org/modules/migration"
	"forgejo.org/modules/process"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/structs"
	"forgejo.org/modules/test"
	app_context "forgejo.org/services/context"
	"forgejo.org/services/forms"
	migrations_allowlist "forgejo.org/services/migrations/allowlist"
	mirror_service "forgejo.org/services/mirror"
	release_service "forgejo.org/services/release"
	repo_service "forgejo.org/services/repository"
	files_service "forgejo.org/services/repository/files"
	"forgejo.org/tests"
	"forgejo.org/tests/forgery"

	"github.com/PuerkitoBio/goquery"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMirrorPull(t *testing.T) {
	t.Run("Basic", func(t *testing.T) {
		defer tests.PrepareTestEnv(t)()

		user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
		repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
		repoPath := repo_model.RepoPath(user.Name, repo.Name)

		// This test is mirroring a local repo, and at one point passed just because it missed any "IsMigrateURLAllowed"
		// check. As one is now rechecked during the migration, to preserve the rest of the logic of this test we enable
		// local imports for user 2 during this test:
		defer test.MockVariableValue(&setting.ImportLocalPaths, true)()
		_, err := db.GetEngine(t.Context()).ID(user.ID).SetExpr("allow_import_local", true).Update(user)
		require.NoError(t, err)
		defer func() { // restore allow_import_local
			_, err := db.GetEngine(t.Context()).ID(user.ID).SetExpr("allow_import_local", false).Update(user)
			require.NoError(t, err)
		}()

		opts := migration.MigrateOptions{
			RepoName:    "test_mirror",
			Description: "Test mirror",
			Private:     false,
			Mirror:      true,
			CloneAddr:   repoPath,
			Wiki:        true,
			Releases:    false,
		}

		mirrorRepo, err := repo_service.CreateRepositoryDirectly(db.DefaultContext, user, user, repo_service.CreateRepoOptions{
			Name:        opts.RepoName,
			Description: opts.Description,
			IsPrivate:   opts.Private,
			IsMirror:    opts.Mirror,
			Status:      repo_model.RepositoryBeingMigrated,
		})
		require.NoError(t, err)
		assert.True(t, mirrorRepo.IsMirror, "expected pull-mirror repo to be marked as a mirror immediately after its creation")

		ctx := t.Context()

		mirror, err := repo_service.MigrateRepositoryGitData(ctx, user, mirrorRepo, opts, nil)
		require.NoError(t, err)

		gitRepo, err := gitrepo.OpenRepository(git.DefaultContext, repo)
		require.NoError(t, err)
		defer gitRepo.Close()

		findOptions := repo_model.FindReleasesOptions{
			IncludeDrafts: true,
			IncludeTags:   true,
			RepoID:        mirror.ID,
		}
		initCount, err := db.Count[repo_model.Release](db.DefaultContext, findOptions)
		require.NoError(t, err)

		require.NoError(t, release_service.CreateRelease(gitRepo, &repo_model.Release{
			RepoID:       repo.ID,
			Repo:         repo,
			PublisherID:  user.ID,
			Publisher:    user,
			TagName:      "v0.2",
			Target:       "master",
			Title:        "v0.2 is released",
			Note:         "v0.2 is released",
			IsDraft:      false,
			IsPrerelease: false,
			IsTag:        true,
		}, "", []*release_service.AttachmentChange{}))

		_, err = repo_model.GetMirrorByRepoID(ctx, mirror.ID)
		require.NoError(t, err)

		ok := mirror_service.SyncPullMirror(ctx, mirror.ID)
		assert.True(t, ok)

		count, err := db.Count[repo_model.Release](db.DefaultContext, findOptions)
		require.NoError(t, err)
		assert.Equal(t, initCount+1, count)

		release, err := repo_model.GetRelease(db.DefaultContext, repo.ID, "v0.2")
		require.NoError(t, err)
		require.NoError(t, release_service.DeleteReleaseByID(ctx, repo, release, user, true))

		ok = mirror_service.SyncPullMirror(ctx, mirror.ID)
		assert.True(t, ok)

		count, err = db.Count[repo_model.Release](db.DefaultContext, findOptions)
		require.NoError(t, err)
		assert.Equal(t, initCount, count)
	})

	// How will we interact with the pull mirror during this test?
	interactionMethod := []struct {
		name                        string
		useAPI                      bool
		createPullMirror            func(t *testing.T, sourceRepo *repo_model.Repository, authenticate bool) (repoName string)
		verifyMirrorDetails         func(t *testing.T, sourceRepo *repo_model.Repository, mirrorName string, authenticate bool)
		triggerPullMirror           func(t *testing.T, mirrorName string)
		changePullMirrorCredentials func(t *testing.T, sourceRepo *repo_model.Repository, mirrorName string, authenticate bool)
		changePullMirrorAddress     func(t *testing.T, sourceRepo *repo_model.Repository, mirrorName string, authenticate bool)
	}{
		{
			name:              "API",
			useAPI:            true,
			createPullMirror:  createPullMirrorViaAPI,
			triggerPullMirror: triggerPullMirrorViaAPI,
			verifyMirrorDetails: func(t *testing.T, sourceRepo *repo_model.Repository, mirrorName string, authenticate bool) {
				// API provides no visibility into a repo's mirror settings right now
			},
		},
		{
			name:                        "Web",
			useAPI:                      false,
			createPullMirror:            createPullMirrorViaWeb,
			triggerPullMirror:           triggerPullMirrorViaWeb,
			verifyMirrorDetails:         verifyPullMirrorViaWeb,
			changePullMirrorCredentials: changePullMirrorCredentialsViaWeb,
			changePullMirrorAddress:     changePullMirrorCredentialsViaWeb, // one endpoint, so same as creds
		},
	}

	mirrorConfiguration := []struct {
		name          string
		privateSource bool
	}{
		{
			name: "HTTP Without Auth",
		},
		{
			name:          "HTTP With Auth",
			privateSource: true,
		},
	}

	// Not using MockVariableValue due to need to undo `migrations_allowlist.Init()`
	prev := setting.Migrations.AllowedDomains
	setting.Migrations.AllowedDomains = "localhost"
	migrations_allowlist.Init() // reinitialize for changed allowList
	defer func() {
		setting.Migrations.AllowedDomains = prev
		migrations_allowlist.Init() // reinitialize for changed allowList
	}()

	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		for _, im := range interactionMethod {
			for _, mc := range mirrorConfiguration {
				t.Run(fmt.Sprintf("%s/%s", im.name, mc.name), func(t *testing.T) {
					defer tests.PrintCurrentTest(t)()

					user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})

					// Create the source repository that will be mirrored.
					var sourceRepoSha string
					sourceRepo := forgery.CreateRepository(t, user2, &forgery.CreateRepositoryOptions{
						IsPrivate: mc.privateSource,
						Files: forgery.MapFS{
							"docs.md": forgery.MapFile("hello, world"),
						},
						LatestSha: &sourceRepoSha,
					})
					require.NotEmpty(t, sourceRepoSha)

					// Create pull mirror
					mirror := im.createPullMirror(t, sourceRepo, mc.privateSource)
					verifyPullMirrorContents(t, mirror, sourceRepoSha)
					verifyPullMirrorConfig(t, mirror, sourceRepo, mc.privateSource)
					im.verifyMirrorDetails(t, sourceRepo, mirror, mc.privateSource)

					// Push a change to the source and refresh the mirror
					sourceRepoSha = changePullMirrorSource(t, sourceRepo, sourceRepoSha)
					im.triggerPullMirror(t, mirror)
					waitForPullMirror(t, mirror, sourceRepoSha)

					// Test changing the mirror's authentication method (if available)
					if mc.privateSource && im.changePullMirrorCredentials != nil {
						sourceRepoSha = changePullMirrorSource(t, sourceRepo, sourceRepoSha)
						im.changePullMirrorCredentials(t, sourceRepo, mirror, mc.privateSource)
						verifyPullMirrorConfig(t, mirror, sourceRepo, mc.privateSource)
						im.verifyMirrorDetails(t, sourceRepo, mirror, mc.privateSource)
						im.triggerPullMirror(t, mirror)
						waitForPullMirror(t, mirror, sourceRepoSha)
					}

					// Test changing the mirror's address (if available)
					if im.changePullMirrorAddress != nil {
						sourceRepo = renamePullMirrorSourceRepo(t, sourceRepo)
						sourceRepoSha = changePullMirrorSource(t, sourceRepo, sourceRepoSha)
						im.changePullMirrorAddress(t, sourceRepo, mirror, mc.privateSource)
						verifyPullMirrorConfig(t, mirror, sourceRepo, mc.privateSource)
						im.verifyMirrorDetails(t, sourceRepo, mirror, mc.privateSource)
						im.triggerPullMirror(t, mirror)
						waitForPullMirror(t, mirror, sourceRepoSha)
					}
				})
			}
		}
	})

	t.Run("migrate from repo config credentials", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})

		mirrorRepo := forgery.CreateRepository(t, user2, nil)

		// Write to the repo a config file that would have plausibly existed before EncryptedRemoteAddress was
		// introduced:
		repoPath := mirrorRepo.RepoPath()
		err := os.WriteFile(path.Join(repoPath, "config"), []byte(`
[core]
	repositoryformatversion = 0
	filemode = true
	bare = true
[remote "origin"]
	url = https://user:password@example.com/org/repo.git
	tagOpt = --no-tags
	fetch = +refs/*:refs/*
	mirror = true
	fetch = +refs/tags/*:refs/tags/*
`), 0o644)
		require.NoError(t, err)

		// Create a Mirror record without an EncryptedRemoteAddress:
		mirror := &repo_model.Mirror{
			RepoID:      mirrorRepo.ID,
			Interval:    8 * time.Hour,
			EnablePrune: true,
		}
		_, err = db.GetEngine(t.Context()).Insert(mirror)
		require.NoError(t, err)
		require.Nil(t, mirror.EncryptedRemoteAddress)

		remoteURL, err := mirror_service.DecryptOrRecoverRemoteAddress(t.Context(), mirror)
		require.NoError(t, err)
		assert.Equal(t, "https://user:password@example.com/org/repo.git", remoteURL.URL.String())

		// EncryptedRemoteAddress should now be populated from the recovery:
		assert.NotNil(t, mirror.EncryptedRemoteAddress)
		maybeDecryptedURL, err := mirror.DecryptRemoteAddress()
		require.NoError(t, err)
		has, decryptedURL := maybeDecryptedURL.Get()
		require.True(t, has)
		assert.Equal(t, "https://user:password@example.com/org/repo.git", decryptedURL)

		// SanitizedRemoteAddress can be fetched:
		maybeSanitizedURL, err := mirror.SanitizedRemoteAddress()
		require.NoError(t, err)
		has, sanitizedURL := maybeSanitizedURL.Get()
		require.True(t, has)
		assert.Equal(t, "https://user@example.com/org/repo.git", sanitizedURL)

		// Database record is updated in the database:
		refetchMirror := unittest.AssertExistsAndLoadBean(t, &repo_model.Mirror{RepoID: mirrorRepo.ID})
		assert.Equal(t, mirror.EncryptedRemoteAddress, refetchMirror.EncryptedRemoteAddress)

		// Config file is rewritten:
		config, err := os.ReadFile(path.Join(repoPath, "config"))
		require.NoError(t, err)
		assert.Equal(t, `
[core]
	repositoryformatversion = 0
	filemode = true
	bare = true
[remote "origin"]
	url = https://user@example.com/org/repo.git
	tagOpt = --no-tags
	fetch = +refs/*:refs/*
	mirror = true
	fetch = +refs/tags/*:refs/tags/*
`, string(config))

		t.Run("modernize", func(t *testing.T) {
			require.NoError(t, mirror_service.ModernizePullMirrorConfig(t.Context(), mirror))
			config, err = os.ReadFile(path.Join(repoPath, "config"))
			require.NoError(t, err)
			assert.Equal(t, `
[core]
	repositoryformatversion = 0
	filemode = true
	bare = true
[remote "origin"]
	url = https://user@example.com/org/repo.git
	tagOpt = --no-tags
	fetch = +refs/*:refs/*
	mirror = true
	fetch = +refs/tags/*:refs/tags/*
[http]
	followRedirects = false
`, string(config))
		})
	})
}

// Verifies that a pull mirror which was created while the remote address was permitted will fail to sync if the
// AllowedDomains configuration later changes such that the remote URL is no longer permitted.
func TestMirrorPullAddressCheck(t *testing.T) {
	// Allow localhost as a migration domain so the mirror can initially be created from the local test server. Not
	// using MockVariableValue due to need to undo `migrations_allowlist.Init()`.
	prev := setting.Migrations.AllowedDomains
	setting.Migrations.AllowedDomains = "localhost"
	migrations_allowlist.Init() // reinitialize for changed allowList
	defer func() {
		setting.Migrations.AllowedDomains = prev
		migrations_allowlist.Init() // reinitialize for changed allowList
	}()

	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		defer tests.PrintCurrentTest(t)()

		user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})

		// Create the source repository that will be mirrored.
		var sourceRepoSha string
		sourceRepo := forgery.CreateRepository(t, user2, &forgery.CreateRepositoryOptions{
			Files: forgery.MapFS{
				"docs.md": forgery.MapFile("hello, world"),
			},
			LatestSha: &sourceRepoSha,
		})
		require.NotEmpty(t, sourceRepoSha)

		// Create the pull mirror while localhost is still an allowed migration domain.
		mirrorName := createPullMirrorViaWeb(t, sourceRepo, false)
		verifyPullMirrorContents(t, mirrorName, sourceRepoSha)

		// Reset the allowed domains to the default, which does not permit localhost.
		setting.Migrations.AllowedDomains = prev
		migrations_allowlist.Init()

		// Re-triggering the mirror should now fail because the remote URL is no longer permitted.
		mirrorRepo, err := repo_model.GetRepositoryByOwnerAndName(t.Context(), "user2", mirrorName)
		require.NoError(t, err)
		ok := mirror_service.SyncPullMirror(t.Context(), mirrorRepo.ID)
		assert.False(t, ok, "expected pull mirror sync to fail because the remote URL is no longer permitted")
	})
}

func createPullMirrorViaWeb(t *testing.T, sourceRepo *repo_model.Repository, authenticate bool) string {
	session := loginUser(t, "user2")

	mirrorName := fmt.Sprintf("pullmirror-%s", sourceRepo.Name)
	form := &forms.MigrateRepoForm{
		CloneAddr: sourceRepo.CloneLink().HTTPS,
		Service:   structs.PlainGitService,
		UID:       2,
		RepoName:  mirrorName,
		Mirror:    true,
	}
	if authenticate {
		form.AuthUsername = "user2"
		form.AuthPassword = getTokenForLoggedInUser(t, session, auth.AccessTokenScopeReadRepository)
	}

	resp := session.MakeRequest(t,
		NewRequestWithJSON(t, "POST", "/repo/migrate", form),
		http.StatusSeeOther)
	location := resp.Header().Get("Location")
	assert.Equal(t, fmt.Sprintf("/user2/pullmirror-%s", sourceRepo.Name), location)

	var lastBody string
	if !assert.Eventuallyf(t,
		func() bool {
			resp := session.MakeRequest(t,
				NewRequest(t, "GET", location),
				http.StatusOK)
			body := resp.Body.String()
			lastBody = body
			// Looking for the repo page to be fully populated indicating that the migration is complete:
			// Check that the committed file is present:
			if !strings.Contains(body, "docs.md") {
				return false
			}
			// Check that the fork button is present:
			if !strings.Contains(body, fmt.Sprintf("/user2/%s/fork", mirrorName)) {
				return false
			}
			return true
		},
		15*time.Second, 1*time.Second,
		"expected migration to complete and repo page to render") {
		t.Logf("last received page body: %s", lastBody)
	}

	return mirrorName
}

func createPullMirrorViaAPI(t *testing.T, sourceRepo *repo_model.Repository, authenticate bool) string {
	session := loginUser(t, "user2")
	apiToken := getTokenForLoggedInUser(t, session, auth.AccessTokenScopeWriteRepository)

	mirrorName := fmt.Sprintf("pullmirror-%s", sourceRepo.Name)
	form := &structs.MigrateRepoOptions{
		CloneAddr: sourceRepo.CloneLink().HTTPS,
		Service:   "git",
		RepoOwner: "user2",
		RepoName:  mirrorName,
		Mirror:    true,
		LFS:       true,
	}
	if authenticate {
		form.AuthUsername = "user2"
		form.AuthPassword = getTokenForLoggedInUser(t, session, auth.AccessTokenScopeReadRepository)
	}

	resp := session.MakeRequest(t,
		NewRequestWithJSON(t, "POST", "/api/v1/repos/migrate", form).AddTokenAuth(apiToken),
		http.StatusCreated)
	var repo structs.Repository
	DecodeJSON(t, resp, &repo)
	assert.NotNil(t, repo)
	assert.True(t, repo.Mirror)
	assert.False(t, repo.Empty)

	return mirrorName
}

func verifyPullMirrorViaWeb(t *testing.T, sourceRepo *repo_model.Repository, mirrorName string, authenticate bool) {
	session := loginUser(t, "user2")
	resp := session.MakeRequest(t,
		NewRequestf(t, "GET", "/user2/%s/settings", mirrorName),
		http.StatusOK)
	htmlDoc := NewHTMLParser(t, resp.Body)
	htmlDoc.AssertAttrEqual(t, "#mirror_address", "value", sourceRepo.CloneLink().HTTPS)
	if authenticate {
		htmlDoc.AssertAttrEqual(t, "#mirror_username", "value", "user2")
		htmlDoc.AssertAttrEqual(t, "#mirror_password", "value", "")
		htmlDoc.AssertAttrEqual(t, "#mirror_password", "placeholder", "(Unchanged)")
	} else {
		htmlDoc.AssertAttrEqual(t, "#mirror_username", "value", "")
		htmlDoc.AssertAttrEqual(t, "#mirror_password", "value", "")
		htmlDoc.AssertAttrEqual(t, "#mirror_password", "placeholder", "(Unset)")
	}

	resp = session.MakeRequest(t,
		NewRequestf(t, "GET", "/user2/%s", mirrorName),
		http.StatusOK)
	htmlDoc = NewHTMLParser(t, resp.Body)
	htmlDoc.AssertElementPredicate(t, ".fork-flag", func(selection *goquery.Selection) {
		text := strings.TrimSpace(selection.Text())
		assert.Contains(t, text, "mirror of")
		assert.Contains(t, text, sourceRepo.CloneLink().HTTPS)
	})
}

func triggerPullMirrorViaWeb(t *testing.T, mirrorName string) {
	session := loginUser(t, "user2")

	resp := session.MakeRequest(t,
		NewRequestWithValues(t, "POST", fmt.Sprintf("/user2/%s/settings", mirrorName), map[string]string{"action": "mirror-sync"}),
		http.StatusSeeOther)
	location := resp.Header().Get("Location")
	assert.Equal(t, fmt.Sprintf("/user2/%s/settings", mirrorName), location)
}

func triggerPullMirrorViaAPI(t *testing.T, mirrorName string) {
	session := loginUser(t, "user2")
	apiToken := getTokenForLoggedInUser(t, session, auth.AccessTokenScopeWriteRepository)

	// Trigger sync...
	session.MakeRequest(t,
		NewRequestf(t, "POST", "/api/v1/repos/user2/%s/mirror-sync", mirrorName).AddTokenAuth(apiToken),
		http.StatusOK)
}

func changePullMirrorCredentialsViaWeb(t *testing.T, sourceRepo *repo_model.Repository, mirrorName string, authenticate bool) {
	session := loginUser(t, "user2")

	form := map[string]string{
		"action":         "mirror",
		"enable_prune":   "on",
		"interval":       "8h0m0s",
		"mirror_address": sourceRepo.CloneLink().HTTPS,
	}
	if authenticate {
		form["mirror_username"] = "user2"
		form["mirror_password"] = getTokenForLoggedInUser(t, session, auth.AccessTokenScopeReadRepository)
	}

	resp := session.MakeRequest(t,
		NewRequestWithValues(t, "POST", fmt.Sprintf("/user2/%s/settings", mirrorName), form),
		http.StatusSeeOther)
	location := resp.Header().Get("Location")
	assert.Equal(t, fmt.Sprintf("/user2/%s/settings", mirrorName), location)
}

func verifyPullMirrorContents(t *testing.T, mirrorName, expectedSha string) {
	session := loginUser(t, "user2")
	apiToken := getTokenForLoggedInUser(t, session, auth.AccessTokenScopeReadRepository)
	resp := session.MakeRequest(t,
		NewRequest(t, "GET", fmt.Sprintf("/api/v1/repos/user2/%s/commits?sha=main&limit=1", mirrorName)).AddTokenAuth(apiToken),
		http.StatusOK)
	var commits []*structs.Commit
	DecodeJSON(t, resp, &commits)
	require.Len(t, commits, 1)
	assert.Equal(t, expectedSha, commits[0].SHA)
}

func waitForPullMirror(t *testing.T, mirrorName, expectedSha string) {
	session := loginUser(t, "user2")
	apiToken := getTokenForLoggedInUser(t, session, auth.AccessTokenScopeReadRepository)

	var commits []*structs.Commit
	if !assert.Eventually(t,
		func() bool {
			resp := session.MakeRequest(t,
				NewRequest(t, "GET", fmt.Sprintf("/api/v1/repos/user2/%s/commits?sha=main&limit=1", mirrorName)).AddTokenAuth(apiToken),
				http.StatusOK)
			DecodeJSON(t, resp, &commits)
			require.Len(t, commits, 1)
			return commits[0].SHA == expectedSha
		},
		15*time.Second, 1*time.Second) {
		t.Logf("sync was supposed to bring repo to commit %s, but observed commits = %#v", expectedSha, commits)
	}
}

func getGitConfig(t *testing.T, configFile, configPath string) string {
	stdout, stderr, err := process.GetManager().Exec("getGitConfig", "git", "config", "--get", "--file", configFile, configPath)
	require.NoError(t, err, "fetch config %s failed: git stderr: %s", configPath, stderr)
	return strings.TrimSpace(stdout)
}

func verifyPullMirrorConfig(t *testing.T, mirrorName string, sourceRepo *repo_model.Repository, authenticate bool) {
	mirrorRepo, err := repo_model.GetRepositoryByOwnerAndName(t.Context(), "user2", mirrorName)
	require.NoError(t, err)

	repoPath := mirrorRepo.RepoPath()
	configPath := path.Join(repoPath, "config")

	expectedURL := sourceRepo.CloneLink().HTTPS
	if authenticate {
		expectedURL = strings.Replace(expectedURL, "http://", "http://user2@", 1)
	}
	assert.Equal(t, expectedURL, getGitConfig(t, configPath, "remote.origin.url"))
	assert.Equal(t, "true", getGitConfig(t, configPath, "remote.origin.mirror"))
	assert.Equal(t, "+refs/tags/*:refs/tags/*", getGitConfig(t, configPath, "remote.origin.fetch"))
	assert.Equal(t, "false", getGitConfig(t, configPath, "http.followRedirects"))
}

func changePullMirrorSource(t *testing.T, sourceRepo *repo_model.Repository, sourceRepoSha string) string {
	user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	resp, err := files_service.ChangeRepoFiles(git.DefaultContext, sourceRepo, user2,
		&files_service.ChangeRepoFilesOptions{
			Files: []*files_service.ChangeRepoFile{
				{
					Operation:     "update",
					TreePath:      "docs.md",
					ContentReader: strings.NewReader(uuid.NewString()),
				},
			},
			Message:   "add files",
			OldBranch: "main",
			NewBranch: "main",
			Author: &files_service.IdentityOptions{
				Name:  user2.Name,
				Email: user2.Email,
			},
			Committer: &files_service.IdentityOptions{
				Name:  user2.Name,
				Email: user2.Email,
			},
			Dates: &files_service.CommitDateOptions{
				Author:    time.Now(),
				Committer: time.Now(),
			},
			LastCommitID: sourceRepoSha,
		})
	require.NoError(t, err)
	assert.NotEmpty(t, resp)
	return resp.Commit.SHA
}

func renamePullMirrorSourceRepo(t *testing.T, sourceRepo *repo_model.Repository) *repo_model.Repository {
	session := loginUser(t, "user2")
	apiToken := getTokenForLoggedInUser(t, session, auth.AccessTokenScopeWriteRepository)

	newName := uuid.NewString()
	session.MakeRequest(t,
		NewRequestWithJSON(t, "PATCH", fmt.Sprintf("/api/v1/repos/user2/%s", sourceRepo.Name),
			&structs.EditRepoOption{
				Name: &newName,
			}).AddTokenAuth(apiToken),
		http.StatusOK)

	newRepo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: sourceRepo.ID})
	assert.Equal(t, newRepo.Name, newName)
	assert.NotEqual(t, newRepo.CloneLink().HTTPS, sourceRepo.CloneLink().HTTPS) // should have changed to new name
	return newRepo
}

func TestPullMirrorRedactCredentials(t *testing.T) {
	defer unittest.OverrideFixtures("tests/integration/fixtures/TestPullMirrorRedactCredentials")()
	defer tests.PrepareTestEnv(t)()

	session := loginUser(t, "user2")
	session.MakeRequest(t, NewRequestWithValues(t, "POST", "/user2/repo1001/settings", map[string]string{
		"action": "mirror-sync",
	}), http.StatusSeeOther)

	flashCookie := session.GetCookie(app_context.CookieNameFlash)
	assert.NotNil(t, flashCookie)
	assert.Equal(t, "info%3DPulling%2Bchanges%2Bfrom%2Bthe%2Bremote%2Bhttps%253A%252F%252Fexample.com%252Fexample%252Fexample.git%2Bat%2Bthe%2Bmoment.", flashCookie.Value)
}

func TestMirrorPullLFS(t *testing.T) {
	// Not using MockVariableValue due to need to undo `migrations_allowlist.Init()`
	prev := setting.Migrations.AllowedDomains
	setting.Migrations.AllowedDomains = "localhost"
	migrations_allowlist.Init() // reinitialize for changed allowList
	defer func() {
		setting.Migrations.AllowedDomains = prev
		migrations_allowlist.Init() // reinitialize for changed allowList
	}()

	defer test.MockVariableValue(&setting.LFS.StartServer, true)()

	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
		session := loginUser(t, user2.LoginName)
		apiToken := getTokenForLoggedInUser(t, session, auth.AccessTokenScopeWriteRepository)

		// Create the source repository that will be mirrored.
		var sourceRepoSha string
		sourceRepo := forgery.CreateRepository(t, user2, &forgery.CreateRepositoryOptions{
			Files: forgery.MapFS{
				"docs.md":        forgery.MapFile("hello, world"),
				".gitattributes": forgery.MapFile("*.txt filter=lfs diff=lfs merge=lfs -text"),
			},
			LatestSha: &sourceRepoSha,
		})
		require.NotEmpty(t, sourceRepoSha)

		// Push a ".txt" file, which the .gitattributes should cause to be treated as an LFS file.
		req := NewRequestWithJSON(t,
			"POST",
			fmt.Sprintf("/api/v1/repos/%s/%s/contents/my-lfs-file.txt", sourceRepo.OwnerName, sourceRepo.Name),
			&structs.CreateFileOptions{
				FileOptions: structs.FileOptions{
					BranchName: sourceRepo.DefaultBranch,
				},
				ContentBase64: base64.StdEncoding.EncodeToString([]byte("Hello!")),
			}).AddTokenAuth(apiToken)
		MakeRequest(t, req, http.StatusCreated)

		// Create pull mirror
		mirror := createPullMirrorViaAPI(t, sourceRepo, false)

		// raw file will be an LFS pointer
		resp := session.MakeRequest(t,
			NewRequest(t, "GET", fmt.Sprintf("/api/v1/repos/user2/%s/raw/%s/my-lfs-file.txt", mirror, sourceRepo.DefaultBranch)).AddTokenAuth(apiToken),
			http.StatusOK)
		assert.True(t, strings.HasPrefix(resp.Body.String(), "version https://git-lfs.github.com/spec/v1"), "my-lfs-file.txt should be stored as an LFS pointer")

		// /media file will be the original correct contents
		resp = session.MakeRequest(t,
			NewRequest(t, "GET", fmt.Sprintf("/api/v1/repos/user2/%s/media/%s/my-lfs-file.txt", mirror, sourceRepo.DefaultBranch)).AddTokenAuth(apiToken),
			http.StatusOK)
		assert.Equal(t, "Hello!", resp.Body.String())

		t.Run("verify http.Transport secure", func(t *testing.T) {
			prevBlocked := setting.Migrations.BlockedDomains
			setting.Migrations.BlockedDomains = "example.com"
			migrations_allowlist.Init() // reinitialize for changed allowList
			defer func() {
				setting.Migrations.BlockedDomains = prevBlocked
				migrations_allowlist.Init() // reinitialize for changed allowList
			}()

			// Push a new LFS object to the source repo so that it is found during the next sync:
			req := NewRequestWithJSON(t,
				"POST",
				fmt.Sprintf("/api/v1/repos/%s/%s/contents/my-second-lfs-file.txt", sourceRepo.OwnerName, sourceRepo.Name),
				&structs.CreateFileOptions{
					FileOptions: structs.FileOptions{
						BranchName: sourceRepo.DefaultBranch,
					},
					ContentBase64: base64.StdEncoding.EncodeToString([]byte("Hello, this is a new file!")),
				}).AddTokenAuth(apiToken)
			MakeRequest(t, req, http.StatusCreated)
			resp := session.MakeRequest(t,
				NewRequest(t, "GET", fmt.Sprintf("/api/v1/repos/user2/%s/raw/%s/my-second-lfs-file.txt", sourceRepo.Name, sourceRepo.DefaultBranch)).AddTokenAuth(apiToken),
				http.StatusOK)

			// Awkwardly this test won't try to download the LFS object because we're syncing between two local
			// repositories and the LFS content store will be shared between them, so the content pointer is already
			// present.  To ensure the LFS client is triggered, delete the content... of course it won't be available to
			// be synced then, but we're expecting an error in this test case.
			pointer, err := lfs.ReadPointerFromBuffer(resp.Body.Bytes())
			require.NoError(t, err)
			contentStore := lfs.NewContentStore()
			contentStore.Delete(pointer.RelativePath())

			// In order to pass the migration URL check, but fail on the LFS endpoint access, we'll reconfigure the LFS
			// endpoint to a different domain name which is prohibited:
			mirrorRepo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{Name: mirror})
			mirrorObj := unittest.AssertExistsAndLoadBean(t, &repo_model.Mirror{RepoID: mirrorRepo.ID})
			mirrorObj.LFSEndpoint = "https://example.com/something"
			_, err = db.GetEngine(t.Context()).ID(mirrorObj.ID).Update(mirrorObj)
			require.NoError(t, err)

			lc, cleanup := test.NewLogChecker(log.DEFAULT, log.ERROR)
			lc.Filter("migration can only call allowed HTTP servers (check your migrations.ALLOWED_DOMAINS/ALLOW_LOCALNETWORKS setting)")
			lc.StopMark("SyncMirrors")
			defer cleanup()

			ok := mirror_service.SyncPullMirror(t.Context(), mirrorRepo.ID)
			assert.True(t, ok) // LFS failure doesn't output a migration failure

			logFiltered, _ := lc.Check(5 * time.Second)
			assert.True(t, logFiltered[0], "expected migration error output")
		})
	})
}
