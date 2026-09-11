// Copyright 2024 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"net/http"
	"strings"
	"testing"

	"forgejo.org/tests"

	"github.com/stretchr/testify/assert"
)

// TestRepoCollaborators is a test for contents of Collaborators tab in the repo settings
// It only covers a few elements and can be extended as needed
func TestRepoCollaborators(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	session := loginUser(t, "user2")

	// Visit Collaborators tab of repo settings
	response := session.MakeRequest(t, NewRequest(t, "GET", "/user2/repo1/settings/collaboration"), http.StatusOK)
	page := NewHTMLParser(t, response.Body).Find(".repo-setting-content")

	// Verify header
	assert.Equal(t, "Collaborators", strings.TrimSpace(page.Find("h4").Text()))

	// Verify button text
	page = page.Find("#repo-collab-form")
	assert.Equal(t, "Add collaborator", strings.TrimSpace(page.Find("button.primary").Text()))

	// Verify placeholder
	placeholder, exists := page.Find("#search-user-box input").Attr("placeholder")
	assert.True(t, exists)
	assert.Equal(t, "Search users…", placeholder)
}

func TestRepoCollaboratorAcessMode(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	session := loginUser(t, "user2")

	t.Run("Add collaboration", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		session.MakeRequest(t, NewRequestWithValues(t, "POST", "/user2/repo1/settings/collaboration", map[string]string{
			"collaborator": "user5",
		}), http.StatusSeeOther)

		response := session.MakeRequest(t, NewRequest(t, "GET", "/user2/repo1/settings/collaboration"), http.StatusOK)
		htmlDoc := NewHTMLParser(t, response.Body)

		assert.Equal(t, "Write", htmlDoc.Find(".access-mode .text").Text())
	})

	test := func(t *testing.T, uid, mode, expectedMode string) {
		t.Helper()
		defer tests.PrintCurrentTest(t, 1)()
		session.MakeRequest(t, NewRequestWithValues(t, "POST", "/user2/repo1/settings/collaboration/access_mode", map[string]string{
			"uid":  uid,
			"mode": mode,
		}), http.StatusOK)

		response := session.MakeRequest(t, NewRequest(t, "GET", "/user2/repo1/settings/collaboration"), http.StatusOK)
		htmlDoc := NewHTMLParser(t, response.Body)
		assert.Equal(t, expectedMode, htmlDoc.Find(".access-mode .text").Text())
	}

	t.Run("None access mode", func(t *testing.T) {
		test(t, "5", "0", "Write")
	})

	t.Run("Read access mode", func(t *testing.T) {
		test(t, "5", "1", "Read")
	})

	t.Run("Write access mode", func(t *testing.T) {
		test(t, "5", "2", "Write")
	})

	t.Run("Admin access mode", func(t *testing.T) {
		test(t, "5", "3", "Administrator")
	})

	t.Run("Owner access mode", func(t *testing.T) {
		// Confirm it didn't change from the previous permission.
		test(t, "5", "4", "Administrator")
	})
}
