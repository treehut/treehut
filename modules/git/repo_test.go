// Copyright 2017 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package git

import (
	"bytes"
	"encoding/base64"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetLatestCommitTime(t *testing.T) {
	bareRepo1Path := filepath.Join(testReposDir, "repo1_bare")
	lct, err := GetLatestCommitTime(DefaultContext, bareRepo1Path)
	require.NoError(t, err)
	// Time is Sun Nov 13 16:40:14 2022 +0100
	// which is the time of commit
	// ce064814f4a0d337b333e646ece456cd39fab612 (refs/heads/master)
	assert.EqualValues(t, 1668354014, lct.Unix())
}

func TestRepoIsEmpty(t *testing.T) {
	emptyRepo2Path := filepath.Join(testReposDir, "repo2_empty")
	repo, err := openRepositoryWithDefaultContext(emptyRepo2Path)
	require.NoError(t, err)
	defer repo.Close()
	isEmpty, err := repo.IsEmpty()
	require.NoError(t, err)
	assert.True(t, isEmpty)
}

func TestRepoGetDivergingCommits(t *testing.T) {
	bareRepo1Path := filepath.Join(testReposDir, "repo1_bare")
	do, err := GetDivergingCommits(t.Context(), bareRepo1Path, "master", "branch2", nil)
	require.NoError(t, err)
	assert.Equal(t, DivergeObject{
		Ahead:  1,
		Behind: 5,
	}, do)

	do, err = GetDivergingCommits(t.Context(), bareRepo1Path, "master", "master", nil)
	require.NoError(t, err)
	assert.Equal(t, DivergeObject{
		Ahead:  0,
		Behind: 0,
	}, do)

	do, err = GetDivergingCommits(t.Context(), bareRepo1Path, "master", "test", nil)
	require.NoError(t, err)
	assert.Equal(t, DivergeObject{
		Ahead:  0,
		Behind: 2,
	}, do)
}

func TestCloneCredentials(t *testing.T) {
	calledWithoutPassword := false
	askpassFile := ""
	credentialsFile := ""

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/info/refs" {
			return
		}

		// Get basic authorization.
		auth, ok := strings.CutPrefix(req.Header.Get("Authorization"), "Basic ")
		if !ok {
			w.Header().Set("WWW-Authenticate", `Basic realm="Forgejo"`)
			http.Error(w, "require credentials", http.StatusUnauthorized)
			return
		}

		rawAuth, err := base64.StdEncoding.DecodeString(auth)
		require.NoError(t, err)

		user, password, ok := bytes.Cut(rawAuth, []byte{':'})
		assert.True(t, ok)

		// First time around Git tries without password (that's specified in the clone URL).
		// It demonstrates it doesn't immediately uses askpass.
		if len(password) == 0 {
			assert.EqualValues(t, "oauth2", user)
			calledWithoutPassword = true

			w.Header().Set("WWW-Authenticate", `Basic realm="Forgejo"`)
			http.Error(w, "require credentials", http.StatusUnauthorized)
			return
		}

		assert.EqualValues(t, "oauth2", user)
		assert.EqualValues(t, "some_token", password)

		tmpDir := os.TempDir()

		// Verify that the askpass implementation was used.
		files, err := fs.Glob(os.DirFS(tmpDir), "forgejo-askpass*")
		require.NoError(t, err)
		for _, fileName := range files {
			fileContent, err := os.ReadFile(filepath.Join(tmpDir, fileName))
			require.NoError(t, err)

			credentialsPath, ok := bytes.CutPrefix(fileContent, []byte(`exec cat `))
			assert.True(t, ok)

			fileContent, err = os.ReadFile(string(credentialsPath))
			require.NoError(t, err)
			assert.EqualValues(t, "some_token", fileContent)

			askpassFile = filepath.Join(tmpDir, fileName)
			credentialsFile = string(credentialsPath)
		}
	}))

	serverURL, err := url.Parse(server.URL)
	require.NoError(t, err)

	serverURL.User = url.UserPassword("oauth2", "some_token")

	require.NoError(t, Clone(t.Context(), serverURL.String(), t.TempDir(), CloneRepoOptions{}))

	assert.True(t, calledWithoutPassword)
	assert.NotEmpty(t, askpassFile)
	assert.NotEmpty(t, credentialsFile)

	// Check that the helper files are gone.
	_, err = os.Stat(askpassFile)
	require.ErrorIs(t, err, fs.ErrNotExist)
	_, err = os.Stat(credentialsFile)
	require.ErrorIs(t, err, fs.ErrNotExist)
}
