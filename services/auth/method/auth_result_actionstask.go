// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package method

import (
	auth_model "forgejo.org/models/auth"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/optional"
	"forgejo.org/services/auth"
)

var _ auth.AuthenticationResult = &actionsTaskTokenAuthenticationResult{}

type actionsTaskTokenAuthenticationResult struct {
	*auth.BaseAuthenticationResult
	user   *user_model.User
	taskID int64
}

func (r *actionsTaskTokenAuthenticationResult) Scope() optional.Option[auth_model.AccessTokenScope] {
	return optional.None[auth_model.AccessTokenScope]()
}

func (r *actionsTaskTokenAuthenticationResult) User() *user_model.User {
	return r.user
}

func (r *actionsTaskTokenAuthenticationResult) ActionsTaskID() optional.Option[int64] {
	return optional.Some(r.taskID)
}
