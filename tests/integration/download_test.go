// Copyright 2018 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"net/http"
	"testing"

	auth_model "forgejo.org/models/auth"
	"forgejo.org/modules/setting"
	"forgejo.org/tests"

	"github.com/stretchr/testify/assert"
)

func TestDownloadByID(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	session := loginUser(t, "user2")

	// Request raw blob
	req := NewRequest(t, "GET", "/user2/repo1/raw/blob/4b4851ad51df6a7d9f25c979345979eaeb5b349f")
	resp := session.MakeRequest(t, req, http.StatusOK)

	assert.Equal(t, "# repo1\n\nDescription for repo1", resp.Body.String())
}

func TestDownloadByIDForSVGUsesSecureHeaders(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	session := loginUser(t, "user2")

	// Request raw blob
	req := NewRequest(t, "GET", "/user2/repo2/raw/blob/6395b68e1feebb1e4c657b4f9f6ba2676a283c0b")
	resp := session.MakeRequest(t, req, http.StatusOK)

	assert.Equal(t, "default-src 'none'; style-src 'unsafe-inline'; sandbox", resp.Header().Get("Content-Security-Policy"))
	assert.Equal(t, "image/svg+xml", resp.Header().Get("Content-Type"))
	assert.Equal(t, "nosniff", resp.Header().Get("X-Content-Type-Options"))
}

func TestDownloadByIDMedia(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	session := loginUser(t, "user2")

	// Request raw blob
	req := NewRequest(t, "GET", "/user2/repo1/media/blob/4b4851ad51df6a7d9f25c979345979eaeb5b349f")
	resp := session.MakeRequest(t, req, http.StatusOK)

	assert.Equal(t, "# repo1\n\nDescription for repo1", resp.Body.String())
}

func TestDownloadByIDMediaForSVGUsesSecureHeaders(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	session := loginUser(t, "user2")

	// Request raw blob
	req := NewRequest(t, "GET", "/user2/repo2/media/blob/6395b68e1feebb1e4c657b4f9f6ba2676a283c0b")
	resp := session.MakeRequest(t, req, http.StatusOK)

	assert.Equal(t, "default-src 'none'; style-src 'unsafe-inline'; sandbox", resp.Header().Get("Content-Security-Policy"))
	assert.Equal(t, "image/svg+xml", resp.Header().Get("Content-Type"))
	assert.Equal(t, "nosniff", resp.Header().Get("X-Content-Type-Options"))
}

func TestDownloadRawTextFileWithoutMimeTypeMapping(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	session := loginUser(t, "user2")

	req := NewRequest(t, "GET", "/user2/repo2/raw/branch/master/test.xml")
	resp := session.MakeRequest(t, req, http.StatusOK)

	assert.Equal(t, "text/plain; charset=utf-8", resp.Header().Get("Content-Type"))
}

func TestDownloadRawTextFileWithMimeTypeMapping(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	setting.MimeTypeMap.Map[".xml"] = "text/xml"
	setting.MimeTypeMap.Enabled = true

	session := loginUser(t, "user2")

	req := NewRequest(t, "GET", "/user2/repo2/raw/branch/master/test.xml")
	resp := session.MakeRequest(t, req, http.StatusOK)

	assert.Equal(t, "text/xml; charset=utf-8", resp.Header().Get("Content-Type"))

	delete(setting.MimeTypeMap.Map, ".xml")
	setting.MimeTypeMap.Enabled = false
}

// Access under `/raw` is permitted for API tokens.  Those API tokens then need to have the read:repository and the
// correct resource scopes to permit access, though.  The below series of tests covers the middleware combinations on
// the entire `/user/repo/raw/*` URL tree as they use a common middleware implementation.
func TestDownloadAccessViaAPITokens(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	t.Run("no read:repository scope", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		session := loginUser(t, "user2")
		allToken := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeReadMisc)

		t.Run("denied public repo1", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()
			req := NewRequest(t, "GET", "/user2/repo1/raw/blob/4b4851ad51df6a7d9f25c979345979eaeb5b349f").AddTokenAuth(allToken)
			MakeRequest(t, req, http.StatusForbidden)
		})
		t.Run("denied private repo2", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()
			req := NewRequest(t, "GET", "/user2/repo2/raw/blob/1032bbf17fbc0d9c95bb5418dabe8f8c99278700").AddTokenAuth(allToken)
			MakeRequest(t, req, http.StatusForbidden)
		})
		// repo16 is a second repo used in fine-grain testing below, so we include it in other tests as a baseline
		t.Run("denied private repo16", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()
			req := NewRequest(t, "GET", "/user2/repo16/raw/blob/69554a64c1e6030f051e5c3f94bfbd773cd6a324").AddTokenAuth(allToken)
			MakeRequest(t, req, http.StatusForbidden)
		})
	})

	t.Run("all access token", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		session := loginUser(t, "user2")
		allToken := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeReadRepository)

		t.Run("allowed public repo1", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()
			req := NewRequest(t, "GET", "/user2/repo1/raw/blob/4b4851ad51df6a7d9f25c979345979eaeb5b349f").AddTokenAuth(allToken)
			resp := MakeRequest(t, req, http.StatusOK)
			assert.Equal(t, "# repo1\n\nDescription for repo1", resp.Body.String())
		})
		t.Run("allowed private repo2", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()
			req := NewRequest(t, "GET", "/user2/repo2/raw/blob/1032bbf17fbc0d9c95bb5418dabe8f8c99278700").AddTokenAuth(allToken)
			resp := MakeRequest(t, req, http.StatusOK)
			assert.Equal(t, "tree ba1aed4e2ea2443d76cec241b96be4ec990852ec\nparent 205ac761f3326a7ebe416e8673760016450b5cec\nauthor Jimmy Praet <jimmy.praet@telenet.be> 1624996449 +0200\ncommitter Jimmy Praet <jimmy.praet@telenet.be> 1624996449 +0200\n\nAdd test.xml\n", resp.Body.String())
		})
		// repo16 is a second repo used in fine-grain testing below, so we include it in other tests as a baseline
		t.Run("allowed private repo16", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()
			req := NewRequest(t, "GET", "/user2/repo16/raw/blob/69554a64c1e6030f051e5c3f94bfbd773cd6a324").AddTokenAuth(allToken)
			resp := MakeRequest(t, req, http.StatusOK)
			assert.Equal(t, "tree 24f83a471f77579fea57bac7255d6e64e70fce1c\nparent 27566bd5738fc8b4e3fef3c5e72cce608537bd95\nauthor User2 <user2@example.com> 1502042309 +0200\ncommitter User2 <user2@example.com> 1502042309 +0200\n\nnot signed commit\n", resp.Body.String())
		})
	})

	t.Run("public-only access token", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		session := loginUser(t, "user2")
		publicOnlyToken := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopePublicOnly, auth_model.AccessTokenScopeReadRepository)

		t.Run("allowed public repo1", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()
			req := NewRequest(t, "GET", "/user2/repo1/raw/blob/4b4851ad51df6a7d9f25c979345979eaeb5b349f").AddTokenAuth(publicOnlyToken)
			resp := MakeRequest(t, req, http.StatusOK)
			assert.Equal(t, "# repo1\n\nDescription for repo1", resp.Body.String())
		})
		t.Run("denied private repo2", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()
			req := NewRequest(t, "GET", "/user2/repo2/raw/blob/1032bbf17fbc0d9c95bb5418dabe8f8c99278700").AddTokenAuth(publicOnlyToken)
			MakeRequest(t, req, http.StatusNotFound)
		})
		t.Run("denied private repo16", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()
			req := NewRequest(t, "GET", "/user2/repo16/raw/blob/69554a64c1e6030f051e5c3f94bfbd773cd6a324").AddTokenAuth(publicOnlyToken)
			MakeRequest(t, req, http.StatusNotFound)
		})
	})

	t.Run("specific repo access token", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		repo2OnlyToken := createFineGrainedRepoAccessToken(t, "user2",
			[]auth_model.AccessTokenScope{auth_model.AccessTokenScopeReadRepository},
			[]int64{2},
		)

		t.Run("allowed public repo1", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()
			req := NewRequest(t, "GET", "/user2/repo1/raw/blob/4b4851ad51df6a7d9f25c979345979eaeb5b349f").AddTokenAuth(repo2OnlyToken)
			resp := MakeRequest(t, req, http.StatusOK)
			assert.Equal(t, "# repo1\n\nDescription for repo1", resp.Body.String())
		})
		t.Run("allowed inside fine-grain repo2", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()
			req := NewRequest(t, "GET", "/user2/repo2/raw/blob/1032bbf17fbc0d9c95bb5418dabe8f8c99278700").AddTokenAuth(repo2OnlyToken)
			resp := MakeRequest(t, req, http.StatusOK)
			assert.Equal(t, "tree ba1aed4e2ea2443d76cec241b96be4ec990852ec\nparent 205ac761f3326a7ebe416e8673760016450b5cec\nauthor Jimmy Praet <jimmy.praet@telenet.be> 1624996449 +0200\ncommitter Jimmy Praet <jimmy.praet@telenet.be> 1624996449 +0200\n\nAdd test.xml\n", resp.Body.String())
		})
		t.Run("denied private outside fine-grain repo16", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()
			req := NewRequest(t, "GET", "/user2/repo16/raw/blob/69554a64c1e6030f051e5c3f94bfbd773cd6a324").AddTokenAuth(repo2OnlyToken)
			MakeRequest(t, req, http.StatusNotFound)
		})
	})
}
