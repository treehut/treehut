// Copyright 2026 The Forgejo Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package integration

import (
	"net/http"
	"testing"

	"forgejo.org/models/organization"
	access_model "forgejo.org/models/perm/access"
	"forgejo.org/models/unittest"
	"forgejo.org/tests"

	"github.com/stretchr/testify/assert"
)

func TestOrgTeamRemoveAllRepositories(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	team := unittest.AssertExistsAndLoadBean(t, &organization.Team{OrgID: 41, LowerName: "team1"})
	unittest.AssertCount(t, &organization.TeamRepo{TeamID: team.ID, RepoID: 61}, 1)
	unittest.AssertCount(t, &access_model.Access{RepoID: 61, UserID: 39}, 1)

	session := loginUser(t, "user40")
	req := NewRequest(t, "POST", "/org/org41/teams/team1/action/repo/removeall")
	resp := session.MakeRequest(t, req, http.StatusSeeOther)
	assert.Equal(t, "/org/org41/teams/team1/repositories", resp.Header().Get("Location"))

	unittest.AssertCount(t, &organization.TeamRepo{TeamID: team.ID}, 0)
	unittest.AssertCount(t, &access_model.Access{RepoID: 61, UserID: 39}, 0) // access should be removed
}
