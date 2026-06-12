// Copyright 2014 The Gogs Authors. All rights reserved.
// Copyright 2019 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package method

import (
	"net/http"
	"net/url"
	"testing"

	"forgejo.org/modules/setting"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_isGitRawOrLFSPath(t *testing.T) {
	tests := []struct {
		path string

		want bool
	}{
		{
			"/owner/repo/git-upload-pack",
			true,
		},
		{
			"/owner/repo/git-receive-pack",
			true,
		},
		{
			"/owner/repo/info/refs",
			true,
		},
		{
			"/owner/repo/HEAD",
			true,
		},
		{
			"/owner/repo/objects/info/alternates",
			true,
		},
		{
			"/owner/repo/objects/info/http-alternates",
			true,
		},
		{
			"/owner/repo/objects/info/packs",
			true,
		},
		{
			"/owner/repo/objects/info/blahahsdhsdkla",
			true,
		},
		{
			"/owner/repo/objects/01/23456789abcdef0123456789abcdef01234567",
			true,
		},
		{
			"/owner/repo/objects/pack/pack-123456789012345678921234567893124567894.pack",
			true,
		},
		{
			"/owner/repo/objects/pack/pack-0123456789abcdef0123456789abcdef0123456.idx",
			true,
		},
		{
			"/owner/repo/raw/branch/foo/fanaso",
			true,
		},
		{
			"/owner/repo/stars",
			false,
		},
		{
			"/notowner",
			false,
		},
		{
			"/owner/repo",
			false,
		},
		{
			"/owner/repo/commit/123456789012345678921234567893124567894",
			false,
		},
		{
			"/owner/repo/releases/download/tag/repo.tar.gz",
			true,
		},
		{
			"/owner/repo/attachments/6d92a9ee-5d8b-4993-97c9-6181bdaa8955",
			true,
		},
	}
	lfsTests := []string{
		"/owner/repo/info/lfs/",
		"/owner/repo/info/lfs/objects/batch",
		"/owner/repo/info/lfs/objects/oid/filename",
		"/owner/repo/info/lfs/objects/oid",
		"/owner/repo/info/lfs/objects",
		"/owner/repo/info/lfs/verify",
		"/owner/repo/info/lfs/locks",
		"/owner/repo/info/lfs/locks/verify",
		"/owner/repo/info/lfs/locks/123/unlock",
	}

	origLFSStartServer := setting.LFS.StartServer

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			req, _ := http.NewRequest("POST", "http://localhost"+tt.path, nil)
			setting.LFS.StartServer = false
			if got := isGitRawOrAttachOrLFSPath(req); got != tt.want {
				t.Errorf("isGitOrLFSPath() = %v, want %v", got, tt.want)
			}
			setting.LFS.StartServer = true
			if got := isGitRawOrAttachOrLFSPath(req); got != tt.want {
				t.Errorf("isGitOrLFSPath() = %v, want %v", got, tt.want)
			}
		})
	}
	for _, tt := range lfsTests {
		t.Run(tt, func(t *testing.T) {
			req, _ := http.NewRequest("POST", tt, nil)
			setting.LFS.StartServer = false
			if got := isGitRawOrAttachOrLFSPath(req); got != setting.LFS.StartServer {
				t.Errorf("isGitOrLFSPath(%q) = %v, want %v, %v", tt, got, setting.LFS.StartServer, gitRawOrAttachPathRe.MatchString(tt))
			}
			setting.LFS.StartServer = true
			if got := isGitRawOrAttachOrLFSPath(req); got != setting.LFS.StartServer {
				t.Errorf("isGitOrLFSPath(%q) = %v, want %v", tt, got, setting.LFS.StartServer)
			}
		})
	}
	setting.LFS.StartServer = origLFSStartServer
}

func TestAuth_isContainerPath(t *testing.T) {
	testCases := []struct {
		name            string
		input           string
		isContainerPath bool
	}{
		{
			name:            "without trailing slash",
			input:           "https://example.com/v2",
			isContainerPath: true,
		},
		{
			name:            "with trailing slash",
			input:           "https://example.com/v2/",
			isContainerPath: true,
		},
		{
			name:            "with additional path components",
			input:           "https://example.com/v2/example/blobs/uploads/",
			isContainerPath: true,
		},
		{
			name:            "without v2",
			input:           "https://example.com/",
			isContainerPath: false,
		},
		{
			name:            "v2 not at the beginning",
			input:           "https://example.com/something/v2/",
			isContainerPath: false,
		},
		{
			name:            "v2 with prefix",
			input:           "https://example.com/abcd-v2/",
			isContainerPath: false,
		},
		{
			name:            "v2 with suffix",
			input:           "https://example.com/v2-abcd/",
			isContainerPath: false,
		},
		{
			name:            "v1",
			input:           "https://example.com/v1/",
			isContainerPath: false,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			inputURL, err := url.Parse(testCase.input)
			require.NoError(t, err)

			request := http.Request{URL: inputURL}

			assert.Equal(t, testCase.isContainerPath, isContainerPath(&request))
		})
	}
}
