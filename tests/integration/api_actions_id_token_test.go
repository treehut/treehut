// Copyright 2025 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package integration

import (
	"crypto/rsa"
	"encoding/base64"
	"fmt"
	"math/big"
	"net/http"
	"testing"

	actions_model "forgejo.org/models/actions"
	"forgejo.org/models/auth"
	"forgejo.org/models/db"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/modules/setting"
	api "forgejo.org/modules/structs"
	actions_service "forgejo.org/services/actions"
	"forgejo.org/tests"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type getTokenResponse struct {
	Value string `json:"value"`
}

func prepareTestEnvActionsIDToken(t *testing.T) func() {
	t.Helper()
	f := tests.PrepareTestEnv(t, 1)
	return f
}

func TestActionsIDToken(t *testing.T) {
	defer prepareTestEnvActionsIDToken(t)()
	task, err := actions_model.GetTaskByID(db.DefaultContext, 48)
	if err != nil {
		t.Fatal(err)
	}
	err = task.LoadAttributes(db.DefaultContext)
	if err != nil {
		t.Fatal(err)
	}

	gitCtx, err := actions_service.GenerateGiteaContext(task.Job.Run, task.Job)
	require.NoError(t, err)

	token, err := actions_service.CreateAuthorizationToken(task, gitCtx, true, &repo_model.ActionsConfig{})
	require.NoError(t, err)
	tokenWithoutOIDCAccess, err := actions_service.CreateAuthorizationToken(task, gitCtx, false, &repo_model.ActionsConfig{})
	require.NoError(t, err)

	// get JWKs information
	req := NewRequest(t, "GET", "/api/actions/.well-known/keys")
	resp := MakeRequest(t, req, http.StatusOK)
	var jwks jwksResponse
	DecodeJSON(t, resp, &jwks)
	require.Len(t, jwks["keys"], 1)
	key := jwks["keys"][0]

	var exponent []byte
	if exponent, err = base64.RawURLEncoding.DecodeString(key["e"]); err != nil {
		t.Fatal(err)
	}

	var modulus []byte
	if modulus, err = base64.RawURLEncoding.DecodeString(key["n"]); err != nil {
		t.Fatal(err)
	}

	pubKey := rsa.PublicKey{
		E: int(big.NewInt(0).SetBytes(exponent).Uint64()),
		N: big.NewInt(0).SetBytes(modulus),
	}

	t.Run("success path", func(t *testing.T) {
		doAssertions := func(aud string, claims map[string]any) {
			assert.Equal(t, "user1", claims["actor"])
			assert.Equal(t, aud, claims["aud"])
			assert.Equal(t, setting.AppURL+"api/actions", claims["iss"])
			assert.Equal(t, "refs/heads/master", claims["ref"])
			assert.Equal(t, "false", claims["ref_protected"])
			assert.Equal(t, "branch", claims["ref_type"])
			assert.Equal(t, "user5/repo4", claims["repository"])
			assert.Equal(t, "user5", claims["repository_owner"])
			assert.Equal(t, "1", claims["run_attempt"])
			assert.Equal(t, "792", claims["run_id"])
			assert.Equal(t, "188", claims["run_number"])
			assert.Equal(t, "c2d72f548424103f01ee1dc02889c1e2bff816b0", claims["sha"])
			assert.Equal(t, "repo:user5-5/repo4-4:ref:refs/heads/master", claims["sub"])
			assert.Equal(t, "artifact.yaml", claims["workflow"])
			assert.Equal(t, "user5/repo4/.forgejo/workflows/artifact.yaml@refs/heads/master", claims["workflow_ref"])
		}

		// Default aud
		req = NewRequest(t, "GET", "/api/actions/_apis/pipelines/workflows/792/idtoken?placeholder=true").AddTokenAuth(token)
		resp = MakeRequest(t, req, http.StatusOK)
		var getResponse getTokenResponse
		DecodeJSON(t, resp, &getResponse)

		claims := jwt.MapClaims{}
		_, err = jwt.ParseWithClaims(getResponse.Value, claims, func(t *jwt.Token) (any, error) {
			return &pubKey, nil
		})
		require.NoError(t, err)

		doAssertions(setting.AppURL+"user5", claims)

		// Custom aud
		req = NewRequest(t, "GET", "/api/actions/_apis/pipelines/workflows/792/idtoken?placeholder=true&audience=testingAud").AddTokenAuth(token)
		resp = MakeRequest(t, req, http.StatusOK)
		DecodeJSON(t, resp, &getResponse)

		claims = jwt.MapClaims{}
		_, err = jwt.ParseWithClaims(getResponse.Value, claims, func(t *jwt.Token) (any, error) {
			return &pubKey, nil
		})
		require.NoError(t, err)

		doAssertions("testingAud", claims)
	})

	t.Run("with token that doesn't support OIDC", func(t *testing.T) {
		req = NewRequest(t, "GET", "/api/actions/_apis/pipelines/workflows/792/idtoken?placeholder=true").AddTokenAuth(tokenWithoutOIDCAccess)
		resp = MakeRequest(t, req, http.StatusInternalServerError)
		assert.Contains(t, resp.Body.String(), "Error runner api parsing custom claims")
		assert.NotContains(t, resp.Body.String(), "value") // must not leak an actual `getTokenResponse`
	})

	t.Run("with no auth header", func(t *testing.T) {
		req = NewRequest(t, "GET", "/api/actions/_apis/pipelines/workflows/792/idtoken?placeholder=true&audience=testingAud")
		resp = MakeRequest(t, req, http.StatusUnauthorized)
		assert.Contains(t, resp.Body.String(), "Bad authorization header")
	})

	t.Run("with bad token format", func(t *testing.T) {
		req = NewRequest(t, "GET", "/api/actions/_apis/pipelines/workflows/792/idtoken?placeholder=true&audience=testingAud").AddTokenAuth("1234567")
		resp = MakeRequest(t, req, http.StatusInternalServerError)
		assert.Contains(t, resp.Body.String(), "Error runner api parsing authorization token")
	})

	t.Run("with invalid task", func(t *testing.T) {
		task, err := actions_model.GetTaskByID(db.DefaultContext, 48)
		if err != nil {
			t.Fatal(err)
		}
		err = task.LoadAttributes(db.DefaultContext)
		if err != nil {
			t.Fatal(err)
		}
		// Change ID to be invalid
		task.ID = 123456

		gitCtx, err := actions_service.GenerateGiteaContext(task.Job.Run, task.Job)
		require.NoError(t, err)

		token, err := actions_service.CreateAuthorizationToken(task, gitCtx, true, &repo_model.ActionsConfig{})
		require.NoError(t, err)

		req = NewRequest(t, "GET", "/api/actions/_apis/pipelines/workflows/abcde/idtoken?placeholder=true&audience=testingAud").AddTokenAuth(token)
		resp = MakeRequest(t, req, http.StatusInternalServerError)
		assert.Contains(t, resp.Body.String(), "Error runner api getting task by ID")
	})

	t.Run("with task that is not running", func(t *testing.T) {
		task, err := actions_model.GetTaskByID(db.DefaultContext, 49)
		if err != nil {
			t.Fatal(err)
		}
		err = task.LoadAttributes(db.DefaultContext)
		if err != nil {
			t.Fatal(err)
		}

		gitCtx, err := actions_service.GenerateGiteaContext(task.Job.Run, task.Job)
		require.NoError(t, err)

		token, err := actions_service.CreateAuthorizationToken(task, gitCtx, true, &repo_model.ActionsConfig{})
		require.NoError(t, err)

		req = NewRequest(t, "GET", "/api/actions/_apis/pipelines/workflows/abcde/idtoken?placeholder=true&audience=testingAud").AddTokenAuth(token)
		resp = MakeRequest(t, req, http.StatusInternalServerError)
		assert.Contains(t, resp.Body.String(), "Error runner api getting task: task is not running")
	})

	t.Run("with mismatched run ID", func(t *testing.T) {
		req = NewRequest(t, "GET", "/api/actions/_apis/pipelines/workflows/123/idtoken?placeholder=true&audience=testingAud").AddTokenAuth(token)
		resp = MakeRequest(t, req, http.StatusBadRequest)
		assert.Contains(t, resp.Body.String(), "run-id does not match")
	})

	t.Run("authorized integration internal issuer", func(t *testing.T) {
		// Create an Authorized Integration which is set-up to be validated with the in-memory Actions' JWT signing key:
		ai := &auth.AuthorizedIntegration{
			UserID: 2,
			Scope:  auth.AccessTokenScopeAll,
			Issuer: "urn:forgejo:authorized-integrations:actions",
			ClaimRules: &auth.ClaimRules{
				Rules: []auth.ClaimRule{
					{
						Claim:      "sub",
						Comparison: auth.ClaimEqual,
						Value:      "repo:user5-5/repo4-4:ref:refs/heads/master",
					},
				},
			},
			ResourceAllRepos: true,
		}
		require.NoError(t, auth.InsertAuthorizedIntegration(t.Context(), ai))

		// Create a JWT from the Actions system:
		var getResponse getTokenResponse
		req = NewRequest(t, "GET", fmt.Sprintf("/api/actions/_apis/pipelines/workflows/792/idtoken?placeholder=true&audience=%s", ai.Audience)).AddTokenAuth(token)
		resp = MakeRequest(t, req, http.StatusOK)
		DecodeJSON(t, resp, &getResponse)

		// Should be able to make a Forgejo API call with the JWT, authenticated by the Authorized Integration:
		req := NewRequest(t, "GET", "/api/v1/user").AddTokenAuth(getResponse.Value)
		resp := MakeRequest(t, req, http.StatusOK)
		var user api.User
		DecodeJSON(t, resp, &user)
		assert.Equal(t, "user2", user.LoginName)
	})
}
