// Copyright 2022 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package container

import (
	"fmt"
	"net/http"

	auth_model "forgejo.org/models/auth"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/log"
	"forgejo.org/modules/optional"
	"forgejo.org/services/auth"
	"forgejo.org/services/packages"
)

var (
	_ auth.Method               = &Auth{}
	_ auth.AuthenticationResult = &containerAuthenticationResult{}
)

type containerAuthenticationResult struct {
	*auth.BaseAuthenticationResult
	user  *user_model.User
	scope optional.Option[auth_model.AccessTokenScope]
}

func (r *containerAuthenticationResult) Scope() optional.Option[auth_model.AccessTokenScope] {
	return r.scope
}

func (r *containerAuthenticationResult) User() *user_model.User {
	return r.user
}

type Auth struct{}

func (a *Auth) Name() string {
	return "container"
}

// Verify extracts the user from the Bearer token
// If it's an anonymous session a ghost user is returned
func (a *Auth) Verify(req *http.Request, w http.ResponseWriter, sess auth.SessionStore) auth.MethodOutput {
	uid, scope, err := packages.ParseAuthorizationToken(req)
	if err != nil {
		log.Trace("ParseAuthorizationToken: %v", err)
		// Errors from ParseAuthorizationToken are almost all from malformed incoming input, which we'll consider an
		// auth failure:
		// - `Authorization` header was present for all cases, so it's not `AuthenticationNotAttempted`
		// - it's not `AuthenticationError` because malformed headers would cause errors, and this is intended for
		//   server errors which should cause 500s
		return &auth.AuthenticationAttemptedIncorrectCredential{Error: err}
	} else if uid == 0 {
		return &auth.AuthenticationNotAttempted{}
	}

	// Propagate scope of the authorization token.
	authScope := optional.None[auth_model.AccessTokenScope]()
	if scope != "" {
		authScope = optional.Some(scope)
	}

	u, err := user_model.GetPossibleUserByID(req.Context(), uid)
	if err != nil {
		return &auth.AuthenticationError{Error: fmt.Errorf("container auth GetPossibleUserByID: %w", err)}
	}

	return &auth.AuthenticationSuccess{Result: &containerAuthenticationResult{user: u, scope: authScope}}
}
