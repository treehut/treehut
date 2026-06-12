// Copyright 2021 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	auth_model "forgejo.org/models/auth"
	unit_model "forgejo.org/models/unit"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/git"
	api "forgejo.org/modules/structs"
	"forgejo.org/modules/util"
	"forgejo.org/tests"
	"forgejo.org/tests/forgery"

	"github.com/PuerkitoBio/goquery"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func assertFileExist(t *testing.T, p string) {
	exist, err := util.IsExist(p)
	require.NoError(t, err)
	if !assert.True(t, exist) {
		dir := filepath.Dir(p)
		t.Logf("Listing files that were present in dir path %s", dir)
		entries, err := os.ReadDir(dir)
		require.NoError(t, err)
		for _, e := range entries {
			t.Logf("file in path %s -> %s", dir, e.Name())
		}
		t.Logf("End of %d entries in directory %s", len(entries), dir)
	}
}

func assertFileEqual(t *testing.T, p string, content []byte) {
	bs, err := os.ReadFile(p)
	require.NoError(t, err)
	assert.Equal(t, content, bs)
}

type (
	RepoWikiMethod    string
	RepoWikiAuth      string
	RepoWikiTarget    string
	RepoWikiOperation string
)

const (
	RepoWikiSSH  RepoWikiMethod = "SSH"
	RepoWikiHTTP RepoWikiMethod = "HTTP"

	RepoWikiAnonymous                 RepoWikiAuth = "Anonymous"
	RepoWikiAuthenticated             RepoWikiAuth = "Authenticated"
	RepoWikiAuthenticatedNonOwnerUser RepoWikiAuth = "Authenticated-NonOwner"

	RepoWikiPublic  RepoWikiTarget = "Public"
	RepoWikiPrivate RepoWikiTarget = "Private"

	RepoWikiRead  RepoWikiOperation = "Read"
	RepoWikiWrite RepoWikiOperation = "Write"
)

func TestRepoWikiGitOperation(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		for _, method := range []RepoWikiMethod{RepoWikiSSH, RepoWikiHTTP} {
			for _, auth := range []RepoWikiAuth{RepoWikiAnonymous, RepoWikiAuthenticated, RepoWikiAuthenticatedNonOwnerUser} {
				for _, target := range []RepoWikiTarget{RepoWikiPublic, RepoWikiPrivate} {
					for _, operation := range []RepoWikiOperation{RepoWikiRead, RepoWikiWrite} {
						t.Run(fmt.Sprintf("%s/%s/%s/%s", method, auth, target, operation), func(t *testing.T) {
							defer tests.PrintCurrentTest(t)()
							doRepoWikiGitOperation(t, u, method, auth, target, operation)
						})
					}
				}
			}
		}
	})
}

func doRepoWikiGitOperation(t *testing.T, serverURL *url.URL, method RepoWikiMethod, auth RepoWikiAuth, target RepoWikiTarget, operation RepoWikiOperation) {
	repo := "repo1"
	if target == RepoWikiPrivate {
		user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
		privateRepo := forgery.CreateRepository(t, user2, &forgery.CreateRepositoryOptions{
			IsPrivate: true,
		})
		forgery.EnableRepoUnits(t, privateRepo, unit_model.TypeWiki)

		session := loginUser(t, user2.LoginName)
		token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteRepository)
		urlStr := fmt.Sprintf("/api/v1/repos/%s/%s/wiki/new", user2.LoginName, privateRepo.Name)
		req := NewRequestWithJSON(t, "POST", urlStr, &api.CreateWikiPageOptions{
			Title:         "Page With Image",
			ContentBase64: base64.StdEncoding.EncodeToString([]byte("# Page With Image\n\n![Gitea Logo](./raw/jpeg.jpg)\n")),
			Message:       "",
		}).AddTokenAuth(token)
		MakeRequest(t, req, http.StatusCreated)

		repo = privateRepo.Name
	}

	dstPath := t.TempDir()
	r := fmt.Sprintf("%suser2/%s.wiki.git", serverURL.String(), repo)
	testURL, err := url.Parse(r)
	require.NoError(t, err)

	if method == RepoWikiHTTP {
		switch auth {
		case RepoWikiAnonymous:
			// no-op
		case RepoWikiAuthenticated:
			testURL.User = url.UserPassword("user2", userPassword)
		case RepoWikiAuthenticatedNonOwnerUser:
			testURL.User = url.UserPassword("user20", userPassword)
		default:
			t.Fatalf("unexpected auth = %s", auth)
		}

		doRepoWikiGitOperationInner(t, testURL, dstPath, auth, target, operation)
	} else if method == RepoWikiSSH {
		var user string
		switch auth {
		case RepoWikiAnonymous:
			t.Skip() // anonymous ssh is not supported
		case RepoWikiAuthenticated:
			user = "user2" // owner of the repo
		case RepoWikiAuthenticatedNonOwnerUser:
			user = "user20" // not the owner of the repo, not a collaborator
		default:
			t.Fatalf("unexpected auth = %s", auth)
		}

		keyname := "my-testing-key"
		withKeyFile(t, keyname, func(keyFile string) {
			baseAPITestContext := NewAPITestContext(t, user, repo, auth_model.AccessTokenScopeWriteUser)
			t.Run("CreateUserKey", doAPICreateUserKey(baseAPITestContext, fmt.Sprintf("test-key-%s", uuid.New().String()), keyFile, func(t *testing.T, pk api.PublicKey) {}))

			baseAPITestContext.Username = "user2" // target repo owner to compose URLs
			baseAPITestContext.Reponame = fmt.Sprintf("%s.wiki", repo)
			testURL = createSSHUrl(baseAPITestContext.GitPath(), testURL)

			doRepoWikiGitOperationInner(t, testURL, dstPath, auth, target, operation)
		})
	} else {
		t.Fatalf("unexpected method = %s", method)
	}
}

func doRepoWikiGitOperationInner(t *testing.T, gitURL *url.URL, dstPath string, auth RepoWikiAuth, target RepoWikiTarget, operation RepoWikiOperation) {
	err := git.CloneWithArgs(t.Context(), git.AllowLFSFiltersArgs(), gitURL.String(), dstPath, git.CloneRepoOptions{})
	if target == RepoWikiPrivate && (auth == RepoWikiAnonymous || auth == RepoWikiAuthenticatedNonOwnerUser) {
		require.Error(t, err, "clone must fail; auth %s shouldn't be able to access private repo")
		return // no other test conditions to satisfy if the clone failed
	}
	require.NoError(t, err, "clone must succeed; auth %s should be able to access a public repo")

	assertFileExist(t, filepath.Join(dstPath, "Page-With-Image.md"))
	assertFileEqual(t, filepath.Join(dstPath, "Page-With-Image.md"), []byte("# Page With Image\n\n![Gitea Logo](./raw/jpeg.jpg)\n"))

	if operation == RepoWikiWrite {
		f, err := os.OpenFile(filepath.Join(dstPath, "Home.md"), os.O_WRONLY|os.O_TRUNC|os.O_CREATE, 0o644)
		defer f.Close()
		require.NoError(t, err)
		_, err = io.WriteString(f, fmt.Sprintf("# Home Page Edited!\n%s", uuid.New().String()))
		require.NoError(t, err)
		err = f.Close()
		require.NoError(t, err)

		err = git.AddChanges(dstPath, true)
		require.NoError(t, err)
		err = git.CommitChanges(dstPath, git.CommitChangesOptions{Message: "Changes made!"})
		require.NoError(t, err)

		// don't use git.Push() because it doesn't support credential helper, and 'origin' would have had its URL saved
		// with the creds stripped in dstPath so we need the credential helper to be configured.
		cmd := git.NewCommand(t.Context())
		if gitURL.Scheme == "http" {
			_, credCleanup, err := cmd.AddAuthCredentialHelperForRemote(gitURL.String())
			require.NoError(t, err)
			defer credCleanup()
		}
		cmd.AddArguments("push", "origin")

		stdout, stderr, err := cmd.RunStdString(&git.RunOpts{
			Dir:     dstPath,
			Timeout: 2 * time.Second,
		})
		if auth == RepoWikiAuthenticated {
			require.NoError(t, err, "stdout = %q, stderr = %q", stdout, stderr)
		} else {
			require.Error(t, err, "push must fail as authentication mode %s doesn't allow write, but succeeded.  stdout = %q, stderr = %q", auth, stdout, stderr)
		}
	}
}

func Test_RepoWikiPages(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	url := "/user2/repo1/wiki/?action=_pages"
	req := NewRequest(t, "GET", url)
	resp := MakeRequest(t, req, http.StatusOK)

	doc := NewHTMLParser(t, resp.Body)
	expectedPagePaths := []string{
		"Home", "Long-Page", "Page-With-Image", "Page-With-Spaced-Name", "Unescaped-File", "XSS",
	}
	doc.Find("tr").Each(func(i int, s *goquery.Selection) {
		firstAnchor := s.Find("a").First()
		href, _ := firstAnchor.Attr("href")
		pagePath := strings.TrimPrefix(href, "/user2/repo1/wiki/")

		assert.Equal(t, expectedPagePaths[i], pagePath)
	})
}
