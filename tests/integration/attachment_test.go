// Copyright 2019 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"bytes"
	"image"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	auth_model "forgejo.org/models/auth"
	repo_model "forgejo.org/models/repo"
	unit_model "forgejo.org/models/unit"
	"forgejo.org/models/unittest"
	"forgejo.org/modules/storage"
	"forgejo.org/modules/test"
	repo_service "forgejo.org/services/repository"
	"forgejo.org/tests"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func generateImg() bytes.Buffer {
	// Generate image
	myImage := image.NewRGBA(image.Rect(0, 0, 32, 32))
	var buff bytes.Buffer
	png.Encode(&buff, myImage)
	return buff
}

func createAttachment(t *testing.T, session *TestSession, repoURL, filename string, buff bytes.Buffer, expectedStatus int) string {
	body := &bytes.Buffer{}

	// Setup multi-part
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", filename)
	require.NoError(t, err)
	_, err = io.Copy(part, &buff)
	require.NoError(t, err)
	err = writer.Close()
	require.NoError(t, err)

	req := NewRequestWithBody(t, "POST", repoURL+"/issues/attachments", body)
	req.Header.Add("Content-Type", writer.FormDataContentType())
	resp := session.MakeRequest(t, req, expectedStatus)

	if expectedStatus != http.StatusOK {
		return ""
	}
	var obj map[string]string
	DecodeJSON(t, resp, &obj)
	return obj["uuid"]
}

func TestCreateAnonymousAttachment(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	session := emptyTestSession(t)
	createAttachment(t, session, "user2/repo1", "image.png", generateImg(), http.StatusSeeOther)
}

func TestCreateIssueAttachment(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	const repoURL = "user2/repo1"
	session := loginUser(t, "user2")
	uuid := createAttachment(t, session, repoURL, "image.png", generateImg(), http.StatusOK)

	req := NewRequest(t, "GET", repoURL+"/issues/new")
	resp := session.MakeRequest(t, req, http.StatusOK)
	htmlDoc := NewHTMLParser(t, resp.Body)

	link, exists := htmlDoc.doc.Find("form#new-issue").Attr("action")
	assert.True(t, exists, "The template has changed")

	postData := map[string]string{
		"title":   "New Issue With Attachment",
		"content": "some content",
		"files":   uuid,
	}

	req = NewRequestWithValues(t, "POST", link, postData)
	resp = session.MakeRequest(t, req, http.StatusOK)
	test.RedirectURL(resp) // check that redirect URL exists

	// Validate that attachment is available
	req = NewRequest(t, "GET", "/attachments/"+uuid)
	session.MakeRequest(t, req, http.StatusOK)

	// anonymous visit should be allowed because user2/repo1 is a public repository
	MakeRequest(t, req, http.StatusOK)
}

func TestGetAttachment(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	adminSession := loginUser(t, "user1")
	user2Session := loginUser(t, "user2")
	user8Session := loginUser(t, "user8")
	emptySession := emptyTestSession(t)
	testCases := []struct {
		name       string
		uuid       string
		createFile bool
		session    *TestSession
		want       int
	}{
		{"LinkedIssueUUID", "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11", true, user2Session, http.StatusOK},
		{"LinkedCommentUUID", "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a17", true, user2Session, http.StatusOK},
		{"linked_release_uuid", "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a19", true, user2Session, http.StatusOK},
		{"NotExistingUUID", "b0eebc99-9c0b-4ef8-bb6d-6bb9bd380a18", false, user2Session, http.StatusNotFound},
		{"FileMissing", "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a18", false, user2Session, http.StatusInternalServerError},
		{"NotLinked", "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a20", true, user2Session, http.StatusNotFound},
		{"NotLinkedAccessibleByUploader", "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a20", true, user8Session, http.StatusOK},
		{"PublicByNonLogged", "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11", true, emptySession, http.StatusOK},
		{"PrivateByNonLogged", "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a12", true, emptySession, http.StatusNotFound},
		{"PrivateAccessibleByAdmin", "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a12", true, adminSession, http.StatusOK},
		{"PrivateAccessibleByUser", "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a12", true, user2Session, http.StatusOK},
		{"RepoNotAccessibleByUser", "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a12", true, user8Session, http.StatusNotFound},
		{"OrgNotAccessibleByUser", "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a21", true, user8Session, http.StatusNotFound},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Write empty file to be available for response
			if tc.createFile {
				_, err := storage.Attachments.Save(repo_model.AttachmentRelativePath(tc.uuid), strings.NewReader("hello world"), -1)
				require.NoError(t, err)
			}
			// Actual test
			req := NewRequest(t, "GET", "/attachments/"+tc.uuid)
			tc.session.MakeRequest(t, req, tc.want)
		})
	}
}

// Access under `/attachments/{uuid}` and `/{user}/{repo}/attachments/{uuid}` is permitted for API tokens.  Those API
// tokens then need to have the read:issue or read:repository and the correct resource scopes to permit access, though.
func TestGetAttachmentViaAPITokens(t *testing.T) {
	defer unittest.OverrideFixtures("tests/integration/fixtures/TestGetAttachmentViaAPITokens")()
	defer tests.PrepareTestEnv(t)()

	// Create attachment data for an attachment added by this test's fixture.
	_, err := storage.Attachments.Save(repo_model.AttachmentRelativePath("d962b49e-e32a-4b72-922d-33b551b629e2"), strings.NewReader("hello universe"), -1)
	require.NoError(t, err)

	// Enable Issues unit on repo 16, one of our test targets.
	repo_service.UpdateRepositoryUnits(t.Context(),
		unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 16}),
		[]repo_model.RepoUnit{{
			RepoID: 16,
			Type:   unit_model.TypeIssues,
		}}, nil)

	t.Run("attachments", func(t *testing.T) {
		t.Run("no read:issue scope", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			session := loginUser(t, "user2")
			allToken := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeReadMisc)

			t.Run("denied public repo1", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()
				req := NewRequest(t, "GET", "/attachments/a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11").AddTokenAuth(allToken)
				MakeRequest(t, req, http.StatusForbidden)
			})
			t.Run("denied private repo2", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()
				req := NewRequest(t, "GET", "/attachments/a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a12").AddTokenAuth(allToken)
				MakeRequest(t, req, http.StatusForbidden)
			})
			// repo16 is a second repo used in fine-grain testing below, so we include it in other tests as a baseline
			t.Run("denied private repo16", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()
				req := NewRequest(t, "GET", "/attachments/d962b49e-e32a-4b72-922d-33b551b629e2").AddTokenAuth(allToken)
				MakeRequest(t, req, http.StatusForbidden)
			})
		})

		t.Run("all access token", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			session := loginUser(t, "user2")
			allToken := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeReadIssue)

			t.Run("allowed public repo1", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()
				req := NewRequest(t, "GET", "/attachments/a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11").AddTokenAuth(allToken)
				resp := MakeRequest(t, req, http.StatusOK)
				assert.Equal(t, "hello world", resp.Body.String())
			})
			t.Run("allowed private repo2", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()
				req := NewRequest(t, "GET", "/attachments/a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a12").AddTokenAuth(allToken)
				resp := MakeRequest(t, req, http.StatusOK)
				assert.Equal(t, "hello world", resp.Body.String())
			})
			// repo16 is a second repo used in fine-grain testing below, so we include it in other tests as a baseline
			t.Run("allowed private repo16", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()
				req := NewRequest(t, "GET", "/attachments/d962b49e-e32a-4b72-922d-33b551b629e2").AddTokenAuth(allToken)
				resp := MakeRequest(t, req, http.StatusOK)
				assert.Equal(t, "hello universe", resp.Body.String())
			})
		})

		t.Run("public-only access token", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()
			session := loginUser(t, "user2")
			publicOnlyToken := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopePublicOnly, auth_model.AccessTokenScopeReadIssue)

			t.Run("allowed public repo1", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()
				req := NewRequest(t, "GET", "/attachments/a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11").AddTokenAuth(publicOnlyToken)
				resp := MakeRequest(t, req, http.StatusOK)
				assert.Equal(t, "hello world", resp.Body.String())
			})
			t.Run("denied private repo2", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()
				req := NewRequest(t, "GET", "/attachments/a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a12").AddTokenAuth(publicOnlyToken)
				MakeRequest(t, req, http.StatusNotFound)
			})
			t.Run("denied private repo16", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()
				req := NewRequest(t, "GET", "/attachments/d962b49e-e32a-4b72-922d-33b551b629e2").AddTokenAuth(publicOnlyToken)
				MakeRequest(t, req, http.StatusNotFound)
			})
		})

		t.Run("specific repo access token", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()
			repo2OnlyToken := createFineGrainedRepoAccessToken(t, "user2",
				[]auth_model.AccessTokenScope{auth_model.AccessTokenScopeReadIssue},
				[]int64{2},
			)

			t.Run("allowed public repo1", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()
				req := NewRequest(t, "GET", "/attachments/a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11").AddTokenAuth(repo2OnlyToken)
				resp := MakeRequest(t, req, http.StatusOK)
				assert.Equal(t, "hello world", resp.Body.String())
			})
			t.Run("allowed inside fine-grain repo2", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()
				req := NewRequest(t, "GET", "/attachments/a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a12").AddTokenAuth(repo2OnlyToken)
				resp := MakeRequest(t, req, http.StatusOK)
				assert.Equal(t, "hello world", resp.Body.String())
			})
			t.Run("denied private outside fine-grain repo16", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()
				req := NewRequest(t, "GET", "/attachments/d962b49e-e32a-4b72-922d-33b551b629e2").AddTokenAuth(repo2OnlyToken)
				MakeRequest(t, req, http.StatusNotFound)
			})
		})
	})

	t.Run("user-repo-attachments", func(t *testing.T) {
		t.Run("no read:issue scope", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			session := loginUser(t, "user2")
			allToken := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeReadMisc)

			t.Run("denied public repo1", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()
				req := NewRequest(t, "GET", "/user2/repo1/attachments/a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11").AddTokenAuth(allToken)
				MakeRequest(t, req, http.StatusForbidden)
			})
			t.Run("denied private repo2", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()
				req := NewRequest(t, "GET", "/user2/repo2/attachments/a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a12").AddTokenAuth(allToken)
				MakeRequest(t, req, http.StatusForbidden)
			})
			// repo16 is a second repo used in fine-grain testing below, so we include it in other tests as a baseline
			t.Run("denied private repo16", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()
				req := NewRequest(t, "GET", "/user2/repo16/attachments/d962b49e-e32a-4b72-922d-33b551b629e2").AddTokenAuth(allToken)
				MakeRequest(t, req, http.StatusForbidden)
			})
		})

		t.Run("all access token", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			session := loginUser(t, "user2")
			allToken := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeReadIssue)

			t.Run("allowed public repo1", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()
				req := NewRequest(t, "GET", "/user2/repo1/attachments/a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11").AddTokenAuth(allToken)
				resp := MakeRequest(t, req, http.StatusOK)
				assert.Equal(t, "hello world", resp.Body.String())
			})
			t.Run("allowed private repo2", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()
				req := NewRequest(t, "GET", "/user2/repo2/attachments/a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a12").AddTokenAuth(allToken)
				resp := MakeRequest(t, req, http.StatusOK)
				assert.Equal(t, "hello world", resp.Body.String())
			})
			// repo16 is a second repo used in fine-grain testing below, so we include it in other tests as a baseline
			t.Run("allowed private repo16", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()
				req := NewRequest(t, "GET", "/user2/repo16/attachments/d962b49e-e32a-4b72-922d-33b551b629e2").AddTokenAuth(allToken)
				resp := MakeRequest(t, req, http.StatusOK)
				assert.Equal(t, "hello universe", resp.Body.String())
			})
		})

		t.Run("public-only access token", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()
			session := loginUser(t, "user2")
			publicOnlyToken := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopePublicOnly, auth_model.AccessTokenScopeReadIssue)

			t.Run("allowed public repo1", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()
				req := NewRequest(t, "GET", "/user2/repo1/attachments/a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11").AddTokenAuth(publicOnlyToken)
				resp := MakeRequest(t, req, http.StatusOK)
				assert.Equal(t, "hello world", resp.Body.String())
			})
			t.Run("denied private repo2", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()
				req := NewRequest(t, "GET", "/user2/repo2/attachments/a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a12").AddTokenAuth(publicOnlyToken)
				MakeRequest(t, req, http.StatusNotFound)
			})
			t.Run("denied private repo16", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()
				req := NewRequest(t, "GET", "/user2/repo16/attachments/d962b49e-e32a-4b72-922d-33b551b629e2").AddTokenAuth(publicOnlyToken)
				MakeRequest(t, req, http.StatusNotFound)
			})
		})

		t.Run("specific repo access token", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()
			repo2OnlyToken := createFineGrainedRepoAccessToken(t, "user2",
				[]auth_model.AccessTokenScope{auth_model.AccessTokenScopeReadIssue},
				[]int64{2},
			)

			t.Run("allowed public repo1", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()
				req := NewRequest(t, "GET", "/user2/repo1/attachments/a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11").AddTokenAuth(repo2OnlyToken)
				resp := MakeRequest(t, req, http.StatusOK)
				assert.Equal(t, "hello world", resp.Body.String())
			})
			t.Run("allowed inside fine-grain repo2", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()
				req := NewRequest(t, "GET", "/user2/repo2/attachments/a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a12").AddTokenAuth(repo2OnlyToken)
				resp := MakeRequest(t, req, http.StatusOK)
				assert.Equal(t, "hello world", resp.Body.String())
			})
			t.Run("denied private outside fine-grain repo16", func(t *testing.T) {
				defer tests.PrintCurrentTest(t)()
				req := NewRequest(t, "GET", "/user2/repo16/attachments/d962b49e-e32a-4b72-922d-33b551b629e2").AddTokenAuth(repo2OnlyToken)
				MakeRequest(t, req, http.StatusNotFound)
			})
		})
	})
}
