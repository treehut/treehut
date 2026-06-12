// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package method

import (
	auth_model "forgejo.org/models/auth"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/optional"
	"forgejo.org/services/auth"
)

var _ auth.AuthenticationResult = &oAuth2JWTAuthenticationResult{}

type oAuth2JWTAuthenticationResult struct {
	*auth.BaseAuthenticationResult
	user  *user_model.User
	scope optional.Option[auth_model.AccessTokenScope]
}

func (*oAuth2JWTAuthenticationResult) IsOAuth2JWTAuthentication() bool {
	return true
}

func (r *oAuth2JWTAuthenticationResult) User() *user_model.User {
	return r.user
}

func (r *oAuth2JWTAuthenticationResult) Scope() optional.Option[auth_model.AccessTokenScope] {
	return r.scope
}
