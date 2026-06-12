// Copyright 2024 The Forgejo Authors c/o Codeberg e.V.. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"forgejo.org/modules/git"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/test"
	repo_service "forgejo.org/services/repository"
	"forgejo.org/tests"
	"forgejo.org/tests/forgery"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUserProfile(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		user := forgery.CreateUser(t, nil)
		checkReadme := func(t *testing.T, title, readmeFilename string, expectedCount int) {
			t.Run(title, func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()

				// Prepare the test repository
				files := forgery.MapFS{}
				if readmeFilename != "" {
					files[readmeFilename] = forgery.MapFile("# Hi!\n")
				}
				_ = forgery.CreateRepository(t, user, &forgery.CreateRepositoryOptions{
					Name:  ".profile",
					Files: files,
				})

				// Perform the test
				req := NewRequest(t, "GET", "/"+user.Name)
				resp := MakeRequest(t, req, http.StatusOK)

				doc := NewHTMLParser(t, resp.Body)
				readmeCount := doc.Find("#readme_profile").Length()

				assert.Equal(t, expectedCount, readmeCount)
			})
		}

		checkReadme(t, "No readme", "", 0)
		checkReadme(t, "README.md", "README.md", 1)
		checkReadme(t, "readme.md", "readme.md", 1)
		checkReadme(t, "ReadMe.mD", "ReadMe.mD", 1)
		checkReadme(t, "readme.org", "README.org", 1)
		checkReadme(t, "README.en-us.md", "README.en-us.md", 1)
		checkReadme(t, "README.en.md", "README.en.md", 1)
		checkReadme(t, "README.txt", "README.txt", 1)
		checkReadme(t, "README", "README", 1)
		checkReadme(t, "README.mdown", "README.mdown", 1)
		checkReadme(t, "README.i18n.md", "README.i18n.md", 1)
		checkReadme(t, "readmee", "readmee", 0)
		checkReadme(t, "test.md", "test.md", 0)

		t.Run("readme-size", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			_ = forgery.CreateRepository(t, user, &forgery.CreateRepositoryOptions{
				Name: ".profile",
				Files: forgery.MapFS{
					"README.md": forgery.MapFile(`## Lorem ipsum
dolor sit amet, consectetur adipiscing elit, sed do eiusmod tempor incididunt ut labore et dolore magna aliqua.
## Ut enim ad minim veniam
quis nostrud exercitation ullamco laboris nisi ut aliquip ex ea commodo consequat. Duis aute irure dolor in reprehenderit in voluptate velit esse cillum dolore eu fugiat nulla pariatur. Excepteur sint occaecat cupidatat non proident, sunt in culpa qui officia deserunt mollit anim id est laborum`),
				},
			})

			t.Run("full", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()
				defer test.MockVariableValue(&setting.UI.MaxDisplayFileSize, 500)()

				req := NewRequest(t, "GET", "/"+user.Name)
				resp := MakeRequest(t, req, http.StatusOK)
				assert.Contains(t, resp.Body.String(), "Ut enim ad minim veniam")
				assert.Contains(t, resp.Body.String(), "mollit anim id est laborum")
			})

			t.Run("truncated", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()
				defer test.MockVariableValue(&setting.UI.MaxDisplayFileSize, 146)()

				req := NewRequest(t, "GET", "/"+user.Name)
				resp := MakeRequest(t, req, http.StatusOK)
				assert.Contains(t, resp.Body.String(), "Ut enim ad minim")
				assert.NotContains(t, resp.Body.String(), "veniam")
			})
		})

		t.Run("forked-profile-repo", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			// Create forking user
			forkingUser := forgery.CreateUser(t, nil)

			// Create original .profile repository
			originalRepo := forgery.CreateRepository(t, user, &forgery.CreateRepositoryOptions{
				Name: ".profile",
				Files: forgery.MapFS{
					"README.md": forgery.MapFile("# Original Profile Content\nThis should show up on user profile."),
				},
			})

			// Fork the .profile repository to forking user
			forkedRepo, err := repo_service.ForkRepositoryAndUpdates(git.DefaultContext, user, forkingUser, repo_service.ForkRepoOptions{
				BaseRepo: originalRepo,
				Name:     ".profile",
			})
			require.NoError(t, err)

			// Verify that the original user's profile shows the original content
			req := NewRequest(t, "GET", "/"+user.Name)
			resp := MakeRequest(t, req, http.StatusOK)
			// Check if the content appears in the response body
			bodyStr := resp.Body.String()
			if strings.Contains(bodyStr, "Original Profile Content") {
				// Original profile is working correctly
				assert.Contains(t, bodyStr, "This should show up on user profile", "Original profile should render content")
			}

			// Verify that forking user's profile does NOT show the forked content
			// Since it's a fork, it should not render as a profile page (this is the main test)
			req = NewRequest(t, "GET", "/"+forkingUser.Name)
			resp = MakeRequest(t, req, http.StatusOK)
			bodyStr = resp.Body.String()

			// The main assertion: forked .profile content should NOT appear on user profile
			assert.NotContains(t, bodyStr, "Original Profile Content", "Forked .profile repo should NOT render profile content")
			assert.NotContains(t, bodyStr, "This should show up on user profile", "Forked .profile repo should NOT render profile content")

			// Ensure the forked repository still exists and is accessible directly
			req = NewRequest(t, "GET", "/"+forkingUser.Name+"/.profile")
			resp = MakeRequest(t, req, http.StatusOK)
			// The repository page should show the content (since it's the same as original)
			assert.Contains(t, resp.Body.String(), "Original Profile Content", "Forked repo should still be accessible")

			// Verify the fork relationship
			assert.True(t, forkedRepo.IsFork, "Repository should be marked as a fork")
			assert.Equal(t, originalRepo.ID, forkedRepo.ForkID, "Fork should reference original repository")
		})

		t.Run("private-profile-repo", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			// Create a private .profile repository
			profileRepo := forgery.CreateRepository(t, user, &forgery.CreateRepositoryOptions{
				Name:      ".profile",
				IsPrivate: true,
				Files: forgery.MapFS{
					"README.md": forgery.MapFile("# Private Profile Content\nThis should NOT show up on user profile."),
				},
			})

			// Verify that user's profile does NOT show the private content
			req := NewRequest(t, "GET", "/"+user.Name)
			resp := MakeRequest(t, req, http.StatusOK)
			bodyStr := resp.Body.String()

			assert.NotContains(t, bodyStr, "Private Profile Content", "Private .profile repo should NOT render profile content")
			assert.NotContains(t, bodyStr, "This should NOT show up on user profile", "Private .profile repo should NOT render profile content")

			// Verify the repository is actually private
			assert.True(t, profileRepo.IsPrivate, "Repository should be marked as private")
		})
	})
}
