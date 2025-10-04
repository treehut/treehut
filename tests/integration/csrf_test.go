// Copyright 2017 The Gitea Authors. All rights reserved.
// Copyright 2025 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"net/http"
	"testing"

	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/tests"

	"github.com/stretchr/testify/assert"
)

func TestCsrfProtection(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	// test web form csrf via form
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	session := loginUser(t, user.Name)
	req := NewRequestWithValues(t, "POST", "/user/settings", map[string]string{
		"_csrf": "fake_csrf",
	})
	resp := session.MakeRequest(t, req, http.StatusBadRequest)
	assert.Contains(t, resp.Body.String(), "Invalid CSRF token")

	// test web form csrf via header. TODO: should use an UI api to test
	req = NewRequest(t, "POST", "/user/settings")
	req.Header.Add("X-Csrf-Token", "fake_csrf")
	resp = session.MakeRequest(t, req, http.StatusBadRequest)
	assert.Contains(t, resp.Body.String(), "Invalid CSRF token")
}

func TestCSRFSafeMethods(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	t.Run("DELETE", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		session := loginUser(t, "user2")
		resp := session.MakeRequest(t, NewRequest(t, "DELETE", "/user2/repo1/projects/1/2"), http.StatusBadRequest)
		assert.Equal(t, "Invalid CSRF token.\n", resp.Body.String())
	})

	t.Run("PUT", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		session := loginUser(t, "user2")
		resp := session.MakeRequest(t, NewRequest(t, "PUT", "/user2/repo1/projects/1/2"), http.StatusBadRequest)
		assert.Equal(t, "Invalid CSRF token.\n", resp.Body.String())
	})
}
