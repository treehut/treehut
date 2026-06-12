// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package integration

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"testing"
	"time"

	unit_model "forgejo.org/models/unit"
	"forgejo.org/modules/git"
	app_context "forgejo.org/services/context"
	"forgejo.org/tests"
	"forgejo.org/tests/forgery"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPullRemoveAutomerge(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		repo := forgery.CreateRepository(t, nil, &forgery.CreateRepositoryOptions{
			Files: forgery.FilesInit{},
		})
		forgery.EnableRepoUnits(t, repo, unit_model.TypeCode, unit_model.TypePullRequests)

		owner := repo.Owner
		ownerSession := loginUser(t, owner.Name)

		dstPath := t.TempDir()
		cloneURL, _ := url.Parse(fmt.Sprintf("%s%s.git", u.String(), repo.FullName()))
		cloneURL.User = url.UserPassword(owner.Name, userPassword)
		require.NoError(t, git.CloneWithArgs(t.Context(), nil, cloneURL.String(), dstPath, git.CloneRepoOptions{}))
		doGitSetRemoteURL(dstPath, "origin", cloneURL)(t)

		require.NoError(t, git.NewCommand(t.Context(), "switch", "-c", "new-fun-fact").Run(&git.RunOpts{Dir: dstPath}))

		require.NoError(t, os.WriteFile(path.Join(dstPath, "README.md"), []byte("The house of representative already had that in 1937."), 0o600))
		require.NoError(t, git.AddChanges(dstPath, true))
		require.NoError(t, git.CommitChanges(dstPath, git.CommitChangesOptions{
			Committer: &git.Signature{
				Email: "user2@example.com",
				Name:  "user2",
				When:  time.Now(),
			},
			Author: &git.Signature{
				Email: "user2@example.com",
				Name:  "user2",
				When:  time.Now(),
			},
			Message: "Update funfact.",
		}))

		require.NoError(t, git.NewCommand(t.Context(), "push", "origin", "HEAD:refs/for/main", "-o", "topic=new-fun-fact").Run(&git.RunOpts{Dir: dstPath}))

		// Create a protected branch rule for automerge.
		ownerSession.MakeRequest(t, NewRequestWithValues(t, "POST", fmt.Sprintf("/%s/settings/branches/edit", repo.FullName()), map[string]string{
			"rule_name":          "main",
			"required_approvals": "1",
		}), http.StatusSeeOther)

		// Start a automerge for new pull request.
		ownerSession.MakeRequest(t, NewRequestWithValues(t, "POST", fmt.Sprintf("/%s/pulls/1/merge", repo.FullName()), map[string]string{
			"merge_message_field":       "I love automation when it works",
			"do":                        "merge",
			"merge_when_checks_succeed": "true",
		}), http.StatusOK)

		t.Run("No permission", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			otherUser := forgery.CreateUser(t, nil)
			otherSession := loginUser(t, otherUser.Name)

			otherSession.MakeRequest(t, NewRequestWithValues(t, "POST", fmt.Sprintf("/%s/pulls/1/cancel_auto_merge", repo.FullName()), nil), http.StatusSeeOther)

			flashCookie := otherSession.GetCookie(app_context.CookieNameFlash)
			assert.NotNil(t, flashCookie)
			assert.Equal(t, "error%3DYou%2Bdo%2Bnot%2Bhave%2Bpermission%2Bto%2Bcancel%2Bthis%2Bpull%2Brequest%2527s%2Bauto%2Bmerge.", flashCookie.Value)
		})

		t.Run("Normal", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()

			ownerSession.MakeRequest(t, NewRequestWithValues(t, "POST", fmt.Sprintf("/%s/pulls/1/cancel_auto_merge", repo.FullName()), nil), http.StatusSeeOther)

			flashCookie := ownerSession.GetCookie(app_context.CookieNameFlash)
			assert.NotNil(t, flashCookie)
			assert.Equal(t, "success%3DThe%2Bauto%2Bmerge%2Bwas%2Bcanceled%2Bfor%2Bthis%2Bpull%2Brequest.", flashCookie.Value)
		})
	})
}
