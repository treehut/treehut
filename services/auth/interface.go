// Copyright 2019 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package auth

import (
	"context"
	"net/http"

	auth_model "forgejo.org/models/auth"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/optional"
	"forgejo.org/modules/session"
	"forgejo.org/modules/web/middleware"
	"forgejo.org/services/authz"
)

// DataStore represents a data store
type DataStore middleware.ContextDataStore

// SessionStore represents a session store
type SessionStore session.Store

// Method represents an authentication method (plugin) for HTTP requests.
type Method interface {
	// Verify tries to validate credentials provided in the request, and returns one of the [MethodOutput] results
	// indicating the result of its validation.
	Verify(http *http.Request, w http.ResponseWriter, sess SessionStore) MethodOutput
}

// When attempting to authenticate with an authentication [Method], one of the MethodOutput implementations must be
// returned. This interface serves as a enum of supported outputs, plus related values for each enum. Outputs are
// [AuthenticationSuccess], [AuthenticationNotAttempted], [AuthenticationAttemptedWithFailure], and
// [AuthenticationError].
type MethodOutput interface {
	isMethodOutput()
}

// Authentication positively identified the incoming request as successfully authenticated with the result in the
// attached [AuthenticationResult]. For example, if a username and password were provided and we confirmed that those
// credentials matched an existing user in the database, then AuthenticationSuccess would be returned.
type AuthenticationSuccess struct {
	Result AuthenticationResult
}

// Authentication method was not found to be applicable for the given request. For example, if a request did not contain
// `Authorization: Basic ...`, then a basic authentication method would return AuthenticationNotAttempted to indicate
// that this method didn't apply to the incoming request.
type AuthenticationNotAttempted struct{}

// Authentication method was attempted against the request and positively identified to be an incorrect credential. For
// example, if a request contained `Authorization: Basic ...`, and the username and password that were provided were
// found to not match any user, AuthenticationAttemptedIncorrectCredential would be returned. This is typically an
// indicator of a `401 Unauthorized ...` response.
type AuthenticationAttemptedIncorrectCredential struct {
	Error error
}

// Authentication was attempted and an unexpected internal error occurred.
type AuthenticationError struct {
	Error error
}

func (*AuthenticationSuccess) isMethodOutput()                      {}
func (*AuthenticationNotAttempted) isMethodOutput()                 {}
func (*AuthenticationAttemptedIncorrectCredential) isMethodOutput() {}
func (*AuthenticationError) isMethodOutput()                        {}

// PasswordAuthenticator represents a source of authentication
type PasswordAuthenticator interface {
	Authenticate(ctx context.Context, user *user_model.User, login, password string) (*user_model.User, error)
}

// LocalTwoFASkipper represents a source of authentication that can skip local 2fa
type LocalTwoFASkipper interface {
	IsSkipLocalTwoFA() bool
}

// SynchronizableSource represents a source that can synchronize users
type SynchronizableSource interface {
	Sync(ctx context.Context, updateExisting bool) error
}

type AuthenticationResult interface {
	// May return `nil` to represent an anonymous, unauthenticated user.
	User() *user_model.User

	// Optional permission scope indicated by the authentication method
	Scope() optional.Option[auth_model.AccessTokenScope]
	// Optional authorization reducer indicated by the authentication method
	Reducer() authz.AuthorizationReducer

	// Identifies if the authentication involved the user's password. If so, and the user has 2FA enabled, some
	// restrictions may be applied.
	IsPasswordAuthentication() bool

	// Identifies if the authentication was performed by a reverse proxy.
	IsReverseProxyAuthentication() bool

	// Identifies specifically that the OAuth2 JWT authentication method was used. If so, some related OAuth2 API
	// endpoints may be accessible that otherwise wouldn't be.
	IsOAuth2JWTAuthentication() bool

	// If authenticated as an Actions task (using ${{ forgejo.token }}), then indicates the specific task that performed
	// the authentication.
	ActionsTaskID() optional.Option[int64]
}

type BaseAuthenticationResult struct{}

func (*BaseAuthenticationResult) IsOAuth2JWTAuthentication() bool {
	return false
}

func (*BaseAuthenticationResult) IsPasswordAuthentication() bool {
	return false
}

func (*BaseAuthenticationResult) IsReverseProxyAuthentication() bool {
	return false
}

func (*BaseAuthenticationResult) ActionsTaskID() optional.Option[int64] {
	return optional.None[int64]()
}

func (*BaseAuthenticationResult) Reducer() authz.AuthorizationReducer {
	return nil
}

func (*BaseAuthenticationResult) Scope() optional.Option[auth_model.AccessTokenScope] {
	return optional.None[auth_model.AccessTokenScope]()
}

type UnauthenticatedResult struct {
	*BaseAuthenticationResult
}

func (*UnauthenticatedResult) User() *user_model.User {
	return nil
}
