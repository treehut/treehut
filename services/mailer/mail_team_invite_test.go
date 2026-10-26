// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package mailer

import (
	"testing"

	org_model "forgejo.org/models/organization"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/optional"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/test"
	"forgejo.org/modules/translation"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTeamNotificationMail_test(t *testing.T) {
	defer test.MockVariableValue(&setting.Admin.DisableRegularOrgCreation, false)()
	defer test.MockVariableValue(&setting.Admin.SendNotificationEmailOnNewUser, true)()
	require.NoError(t, unittest.PrepareTestDatabase())
	inviter := &user_model.User{
		Name:                    "new_user",
		Language:                "en_US",
		Email:                   "new_user@example.com",
		LastLoginUnix:           1693648327,
		CreatedUnix:             1693648027,
		AllowCreateOrganization: true,
	}
	require.NoError(t, user_model.CreateUser(t.Context(), inviter, &user_model.CreateUserOverwriteOptions{AllowCreateOrganization: optional.Some(true)}))
	org := &org_model.Organization{
		Name:     "new_org",
		IsActive: true,
	}
	require.NoError(t, org_model.CreateOrganization(t.Context(), org, inviter))
	o, err := org_model.GetOrgByName(t.Context(), org.Name)
	require.NoError(t, err)
	team, err := o.GetOwnerTeam(t.Context())
	require.NoError(t, err)
	t.Run("with invitee", func(t *testing.T) {
		invitedUser := &user_model.User{
			Name:          "invited_user",
			Language:      "de-DE",
			Email:         "invited_user@example.com",
			LastLoginUnix: 1693648327,
			CreatedUnix:   1693648027,
		}
		require.NoError(t, user_model.CreateUser(t.Context(), invitedUser))
		invite, err := org_model.CreateTeamInviteForUser(t.Context(), inviter, invitedUser, team)
		require.NoError(t, err)

		called := false
		defer MockMailSettings(func(msgs ...*Message) {
			assert.Len(t, msgs, 1, "Test provides only one user, so only one email must be sent")
			assert.Equal(t, invitedUser.Email, msgs[0].To, "checks if the recipient is invited user")
			locale := translation.NewLocale("de-DE")
			subject := locale.TrString("mail.team_invite.subject", inviter.DisplayName(), org.DisplayName())
			assert.Equal(t, msgs[0].Subject, subject)
			AssertTranslatedLocale(t, msgs[0].Subject, "mail.team_invite.subject")
			called = true
		})()

		MailTeamInvite(t.Context(), inviter, team, invite)

		assert.True(t, called)
	})

	t.Run("without invitee", func(t *testing.T) {
		invite, err := org_model.CreateTeamInviteByEmail(t.Context(), inviter, team, "non-user@example.com")
		require.NoError(t, err)

		called := false
		defer MockMailSettings(func(msgs ...*Message) {
			assert.Len(t, msgs, 1, "Test provides only one user, so only one email must be sent")
			assert.Equal(t, "non-user@example.com", msgs[0].To, "checks if the recipient is invited user")
			locale := translation.NewLocale("en_US")
			subject := locale.TrString("mail.team_invite.subject", inviter.DisplayName(), org.DisplayName())
			assert.Equal(t, msgs[0].Subject, subject)
			AssertTranslatedLocale(t, msgs[0].Subject, "mail.team_invite.subject")
			called = true
		})()

		MailTeamInvite(t.Context(), inviter, team, invite)

		assert.True(t, called)
	})
}
