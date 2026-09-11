// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package integration

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"forgejo.org/modules/setting"
	"forgejo.org/modules/test"
	"forgejo.org/modules/util"
	"forgejo.org/tests"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManifestJson(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	session := loginUser(t, "user2")
	req := NewRequest(t, "GET", "/manifest.json")
	resp := session.MakeRequest(t, req, http.StatusOK)

	data := make(map[string]any)
	DecodeJSON(t, resp, &data)

	assert.Equal(t, setting.AppName, data["name"])
	assert.Equal(t, setting.AppName, data["short_name"])
	assert.Equal(t, setting.AppURL, data["start_url"])
	assert.NotContains(t, data, "display")
}

func TestManifestJsonStandalone(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	defer test.MockVariableValue(&setting.PWA.Standalone, true)()

	session := loginUser(t, "user2")
	req := NewRequest(t, "GET", "/manifest.json")
	resp := session.MakeRequest(t, req, http.StatusOK)

	data := make(map[string]any)
	DecodeJSON(t, resp, &data)

	assert.Equal(t, setting.AppName, data["name"])
	assert.Equal(t, setting.AppName, data["short_name"])
	assert.Equal(t, setting.AppURL, data["start_url"])
	assert.Contains(t, data, "display")
	assert.Equal(t, "standalone", data["display"])
}

func TestManifestJsonCustomFile(t *testing.T) {
	require.NoError(t, os.MkdirAll(util.FilePathJoinAbs(setting.CustomPath, "public"), 0o777))
	manifestPath := util.FilePathJoinAbs(setting.CustomPath, "public/manifest.json")
	file, err := os.OpenFile(manifestPath, os.O_CREATE|os.O_RDWR, 0o777)
	require.NoError(t, err)
	_, err = file.Write([]byte(`{"name":"MyCustomJson"}`))
	require.NoError(t, err)
	require.NoError(t, file.Close())
	defer os.Remove(manifestPath)

	defer tests.PrepareTestEnv(t)()

	session := loginUser(t, "user2")
	req := NewRequest(t, "GET", "/manifest.json")
	resp := session.MakeRequest(t, req, http.StatusOK)

	data := make(map[string]any)
	DecodeJSON(t, resp, &data)

	assert.Equal(t, "MyCustomJson", data["name"])
	assert.NotContains(t, data, "short_name")
	assert.NotContains(t, data, "start_url")
	assert.NotContains(t, data, "display")
}

func TestBaseTemplateTitle(t *testing.T) {
	siteTitleText := "Forgejo: Beyond coding. We Forge."
	defer tests.PrepareTestEnv(t)()

	for _, testCase := range []struct {
		urlPath       string
		expectedTitle string
		userSession   *TestSession
	}{
		{"/user2/-/projects/4", "project on user2", nil},
		{"/user2/repo1/projects/1", "First project - user2/repo1", nil},
		{"/org3/-/projects/7", "project on org3 - org3", nil},
		{"/org6", "Org Six", nil},
		{"/org17/big_test_public_4", "org17/big_test_public_4", nil},
		{"/admin", "Dashboard", loginUser(t, "user1")},
		{"/explore/repos", "Explore", nil}, // /explore is a redirect
		{"/", "", nil},
	} {
		t.Run(testCase.urlPath, func(t *testing.T) {
			urlPath := strings.TrimPrefix(testCase.urlPath, "/")
			req := NewRequest(t, "GET", urlPath)
			var resp *httptest.ResponseRecorder
			if testCase.userSession == nil {
				resp = MakeRequest(t, req, http.StatusOK)
			} else {
				resp = testCase.userSession.MakeRequest(t, req, http.StatusOK)
			}

			htmlDoc := NewHTMLParser(t, resp.Body)

			titleTags := htmlDoc.Find("html > head > title")
			assert.Equal(t, 1, titleTags.Length())
			titleText := titleTags.Text()
			var expectedTitleText string
			if testCase.expectedTitle != "" {
				expectedTitleText = fmt.Sprintf("%s - %s", testCase.expectedTitle, siteTitleText)
			} else {
				expectedTitleText = siteTitleText
			}
			assert.Equal(t, expectedTitleText, titleText)
		})
	}
}
