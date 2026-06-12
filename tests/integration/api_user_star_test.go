// Copyright 2022 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"fmt"
	"net/http"
	"testing"

	auth_model "forgejo.org/models/auth"
	"forgejo.org/modules/setting"
	api "forgejo.org/modules/structs"
	"forgejo.org/modules/test"
	"forgejo.org/routers"
	"forgejo.org/tests"

	"github.com/stretchr/testify/assert"
)

func TestAPIStar(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	user := "user1"
	repo := "user2/repo1"

	session := loginUser(t, user)
	token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeReadUser)
	tokenWithUserScope := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteUser, auth_model.AccessTokenScopeWriteRepository)

	assertDisabledStarsNotFound := func(t *testing.T, req *RequestWrapper) {
		t.Helper()

		defer tests.PrintCurrentTest(t)()
		defer test.MockVariableValue(&setting.Repository.DisableStars, true)()
		defer test.MockVariableValue(&testWebRoutes, routers.NormalRoutes())()

		MakeRequest(t, req, http.StatusNotFound)
	}

	t.Run("Star", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		req := NewRequest(t, "PUT", fmt.Sprintf("/api/v1/user/starred/%s", repo)).
			AddTokenAuth(tokenWithUserScope)
		MakeRequest(t, req, http.StatusNoContent)

		t.Run("disabled stars", func(t *testing.T) {
			assertDisabledStarsNotFound(t, req)
		})
	})

	t.Run("GetStarredRepos", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		req := NewRequest(t, "GET", fmt.Sprintf("/api/v1/users/%s/starred", user)).
			AddTokenAuth(token)
		resp := MakeRequest(t, req, http.StatusOK)

		assert.Equal(t, "1", resp.Header().Get("X-Total-Count"))

		var repos []api.Repository
		DecodeJSON(t, resp, &repos)
		assert.Len(t, repos, 1)
		assert.Equal(t, repo, repos[0].FullName)
		assert.Equal(t, "Go", repos[0].Language)

		t.Run("disabled stars", func(t *testing.T) {
			assertDisabledStarsNotFound(t, req)
		})
	})

	t.Run("GetMyStarredRepos", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		req := NewRequest(t, "GET", "/api/v1/user/starred").
			AddTokenAuth(tokenWithUserScope)
		resp := MakeRequest(t, req, http.StatusOK)

		assert.Equal(t, "1", resp.Header().Get("X-Total-Count"))

		var repos []api.Repository
		DecodeJSON(t, resp, &repos)
		assert.Len(t, repos, 1)
		assert.Equal(t, repo, repos[0].FullName)
		assert.Equal(t, "Go", repos[0].Language)

		t.Run("disabled stars", func(t *testing.T) {
			assertDisabledStarsNotFound(t, req)
		})
	})

	t.Run("IsStarring", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		req := NewRequest(t, "GET", fmt.Sprintf("/api/v1/user/starred/%s", repo)).
			AddTokenAuth(tokenWithUserScope)
		MakeRequest(t, req, http.StatusNoContent)

		req = NewRequest(t, "GET", fmt.Sprintf("/api/v1/user/starred/%s", repo+"notexisting")).
			AddTokenAuth(tokenWithUserScope)
		MakeRequest(t, req, http.StatusNotFound)

		t.Run("disabled stars", func(t *testing.T) {
			assertDisabledStarsNotFound(t, req)
		})
	})

	t.Run("Unstar", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		req := NewRequest(t, "DELETE", fmt.Sprintf("/api/v1/user/starred/%s", repo)).
			AddTokenAuth(tokenWithUserScope)
		MakeRequest(t, req, http.StatusNoContent)

		t.Run("disabled stars", func(t *testing.T) {
			assertDisabledStarsNotFound(t, req)
		})
	})
}

func TestAPIStarRepoAccessTokenResources(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	var repos []api.Repository

	// Test cases repo1, repo2, repo16 -- create a star on each of them so that we can inspect through /starred and see
	// if the repos are visible or not with different access tokens.
	session := loginUser(t, "user2")
	writeToken := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteUser, auth_model.AccessTokenScopeWriteRepository)
	for _, r := range []string{"repo1", "repo2", "repo16"} {
		MakeRequest(t,
			NewRequest(t, "PUT", fmt.Sprintf("/api/v1/user/starred/user2/%s", r)).AddTokenAuth(writeToken),
			http.StatusNoContent)
	}

	find := func() (bool, bool, bool) {
		foundRepo1 := false  // public repo1
		foundRepo2 := false  // private repo2
		foundRepo16 := false // second public repo used in fine-grain testing, included as baseline
		for _, repo := range repos {
			switch repo.Name {
			case "repo1":
				foundRepo1 = true
			case "repo2":
				foundRepo2 = true
			case "repo16":
				foundRepo16 = true
			}
		}
		return foundRepo1, foundRepo2, foundRepo16
	}

	t.Run("all access token", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		allToken := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeReadUser)

		req := NewRequest(t, "GET", "/api/v1/users/user2/starred").AddTokenAuth(allToken)
		resp := MakeRequest(t, req, http.StatusOK)
		DecodeJSON(t, resp, &repos)
		foundRepo1, foundRepo2, foundRepo16 := find()

		assert.True(t, foundRepo1)  // public repo1
		assert.True(t, foundRepo2)  // private repo2
		assert.True(t, foundRepo16) // private repo16, used in fine-grain testing, included as baseline
	})

	t.Run("public-only access token", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		publicOnlyToken := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopePublicOnly, auth_model.AccessTokenScopeReadUser)

		req := NewRequest(t, "GET", "/api/v1/users/user2/starred").AddTokenAuth(publicOnlyToken)
		resp := MakeRequest(t, req, http.StatusOK)
		DecodeJSON(t, resp, &repos)
		foundRepo1, foundRepo2, foundRepo16 := find()

		assert.True(t, foundRepo1)   // public repo1
		assert.False(t, foundRepo2)  // private repo2
		assert.False(t, foundRepo16) // private repo16
	})

	t.Run("specific repo access token", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		repo2OnlyToken := createFineGrainedRepoAccessToken(t, "user2",
			[]auth_model.AccessTokenScope{auth_model.AccessTokenScopeReadUser},
			[]int64{2},
		)

		req := NewRequest(t, "GET", "/api/v1/users/user2/starred").AddTokenAuth(repo2OnlyToken)
		resp := MakeRequest(t, req, http.StatusOK)
		DecodeJSON(t, resp, &repos)
		foundRepo1, foundRepo2, foundRepo16 := find()

		assert.True(t, foundRepo1)   // public repo1, allowed as it's public and read-access only
		assert.True(t, foundRepo2)   // private repo2, allowed inside fine-grain
		assert.False(t, foundRepo16) // private repo16, denied outside fine-grain
	})
}
