// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package method

import (
	user_model "forgejo.org/models/user"
	"forgejo.org/services/auth"
)

var _ auth.AuthenticationResult = &reverseProxyAuthenticationResult{}

type reverseProxyAuthenticationResult struct {
	*auth.BaseAuthenticationResult
	user *user_model.User
}

func (r *reverseProxyAuthenticationResult) User() *user_model.User {
	return r.user
}

func (*reverseProxyAuthenticationResult) IsReverseProxyAuthentication() bool {
	return true
}
