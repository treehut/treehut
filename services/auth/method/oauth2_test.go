// Copyright 2024 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package method

import (
	"net/http"
	"testing"

	actions_model "forgejo.org/models/actions"
	"forgejo.org/models/auth"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/services/actions"
	auth_service "forgejo.org/services/auth"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUserIDFromToken(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	t.Run("Actions JWT", func(t *testing.T) {
		const RunningTaskID = 47
		task := &actions_model.ActionTask{
			ID: RunningTaskID,
			Job: &actions_model.ActionRunJob{
				ID:    2,
				RunID: 1,
			},
		}
		token, err := actions.CreateAuthorizationToken(task, map[string]any{}, false)
		require.NoError(t, err)

		o := OAuth2{}
		output := o.userIDFromToken(t.Context(), token)
		ar, authSuccess := output.(*auth_service.AuthenticationSuccess)
		require.True(t, authSuccess, "expected type AuthenticationSuccess, but was: %#v", output)
		authResult := ar.Result
		assert.Equal(t, int64(user_model.ActionsUserID), authResult.User().ID)
		isActionsToken, authTaskID := authResult.ActionsTaskID().Get()
		assert.True(t, isActionsToken)
		assert.Equal(t, int64(RunningTaskID), authTaskID)
	})

	t.Run("Actions error-JWT", func(t *testing.T) {
		cases := map[string]struct {
			Token string
			Error error
		}{
			"Empty":    {"", auth.ErrAccessTokenEmpty{}},
			"To short": {"abc", auth.ErrAccessTokenNotExist{Token: "abc"}},
		}

		o := OAuth2{}
		for name, c := range cases {
			t.Run(name, func(t *testing.T) {
				output := o.userIDFromToken(t.Context(), c.Token)

				ar, authFailure := output.(*auth_service.AuthenticationAttemptedIncorrectCredential)
				require.True(t, authFailure, "expected type AuthenticationAttemptedIncorrectCredential, but was: %#v", output)
				err := ar.Error

				require.ErrorIs(t, err, c.Error)
			})
		}
	})
}

func TestCheckTaskIsRunning(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	cases := map[string]struct {
		TaskID   int64
		Expected bool
	}{
		"Running":   {TaskID: 47, Expected: true},
		"Missing":   {TaskID: 1, Expected: false},
		"Cancelled": {TaskID: 46, Expected: false},
	}

	for name := range cases {
		c := cases[name]
		t.Run(name, func(t *testing.T) {
			actual := CheckTaskIsRunning(t.Context(), c.TaskID)
			assert.Equal(t, c.Expected, actual)
		})
	}
}

func TestParseToken(t *testing.T) {
	cases := map[string]struct {
		Header        string
		ExpectedToken string
		Expected      bool
	}{
		"Token Uppercase":   {Header: "Token 1234567890123456789012345687901325467890", ExpectedToken: "1234567890123456789012345687901325467890", Expected: true},
		"Token Lowercase":   {Header: "token 1234567890123456789012345687901325467890", ExpectedToken: "1234567890123456789012345687901325467890", Expected: true},
		"Token Unicode":     {Header: "to\u212Aen 1234567890123456789012345687901325467890", ExpectedToken: "", Expected: false},
		"Bearer Uppercase":  {Header: "Bearer 1234567890123456789012345687901325467890", ExpectedToken: "1234567890123456789012345687901325467890", Expected: true},
		"Bearer Lowercase":  {Header: "bearer 1234567890123456789012345687901325467890", ExpectedToken: "1234567890123456789012345687901325467890", Expected: true},
		"Missing type":      {Header: "1234567890123456789012345687901325467890", ExpectedToken: "", Expected: false},
		"Three Parts":       {Header: "abc 1234567890 test", ExpectedToken: "", Expected: false},
		"Token Three Parts": {Header: "Token 1234567890 test", ExpectedToken: "", Expected: false},
	}

	for name := range cases {
		c := cases[name]
		t.Run(name, func(t *testing.T) {
			req, _ := http.NewRequest("GET", "/", nil)
			req.Header.Add("Authorization", c.Header)
			ActualToken, ActualSuccess := parseToken(req)
			assert.Equal(t, c.ExpectedToken, ActualToken)
			assert.Equal(t, c.Expected, ActualSuccess)
		})
	}
}
