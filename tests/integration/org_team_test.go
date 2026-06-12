// Copyright 2026 The Forgejo Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package integration

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"forgejo.org/models/db"
	"forgejo.org/models/organization"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/test"
	"forgejo.org/tests"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPaginatedMembers(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	// To make sure that pagination kicks in even though the test team has few members
	defer test.MockVariableValue(&setting.UI.MembersPagingNum, 2)()

	org := unittest.AssertExistsAndLoadBean(t, &organization.Organization{ID: 17})
	team := unittest.AssertExistsAndLoadBean(t, &organization.Team{ID: 9})
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 29})

	assert.GreaterOrEqual(t, org.NumMembers, 3)
	isOrgMember, err := organization.IsOrganizationMember(db.DefaultContext, org.ID, user.ID)
	require.NoError(t, err)
	assert.True(t, isOrgMember)
	isTeamMember, err := organization.IsTeamMember(db.DefaultContext, team.OrgID, team.ID, user.ID)
	require.NoError(t, err)
	assert.True(t, isTeamMember)
	assert.Equal(t, org.ID, team.OrgID)

	session := loginUser(t, user.Name)

	teamURL := fmt.Sprintf("/org/%s/teams/%s", org.Name, team.LowerName)
	newVar := session.MakeRequest(t, NewRequest(t, "GET", teamURL), http.StatusOK).Body
	doc := NewHTMLParser(t, newVar)
	assert.Contains(t, strings.TrimSpace(doc.Find("a.item.navigation:contains('Next')").AttrOr("href", "")), fmt.Sprintf("%s?page=2", teamURL))
}
