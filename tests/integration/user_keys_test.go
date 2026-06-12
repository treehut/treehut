// Copyright 2025 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package integration

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"forgejo.org/modules/test"
	"forgejo.org/tests"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVerifySSHkeyPage(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	// user2 has an SSH key in fixtures to test this on
	session := loginUser(t, "user2")

	page := NewHTMLParser(t, session.MakeRequest(t, NewRequest(t, "GET", "/user/settings/keys"), http.StatusOK).Body)
	link, exists := page.Find("#keys-ssh a.button[href^='?verify_ssh=']").Attr("href")
	assert.True(t, exists)

	page = NewHTMLParser(t, session.MakeRequest(t, NewRequest(t, "GET", fmt.Sprintf("/user/settings/keys%s", link)), http.StatusOK).Body)

	// The hint contains a link to the same page the user is at now to get it reloaded if followed
	linkShown, exists := page.Find("#keys-ssh form[action='/user/settings/keys'] .help a").Attr("href")
	assert.True(t, exists)
	// QueryUnescape links before comparison, because they contain "%3a" versus "%3A", both unescaping to ":"
	linkUnescaped, err := url.QueryUnescape(link)
	require.NoError(t, err)
	linkShownUnescaped, err := url.QueryUnescape(linkShown)
	require.NoError(t, err)
	assert.Equal(t, linkUnescaped, linkShownUnescaped)

	// The token changes every minute, we can avoid this sleep via timeutil and mocking.
	test.SleepTillNextMinute()

	// Verify that if you refresh it via the link another token is shown.
	token, exists := page.Find("#keys-ssh form input[readonly]").Attr("value")
	assert.True(t, exists)

	page = NewHTMLParser(t, session.MakeRequest(t, NewRequestf(t, "GET", "/user/settings/keys%s", linkShown), http.StatusOK).Body)
	otherToken, exists := page.Find("#keys-ssh form .field input[readonly]").Attr("value")
	assert.True(t, exists)
	assert.NotEqual(t, token, otherToken)
}
