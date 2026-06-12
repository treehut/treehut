// Copyright 2014 The Gogs Authors. All rights reserved.
// Copyright 2019 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package method

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	actions_model "forgejo.org/models/actions"
	auth_model "forgejo.org/models/auth"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/base"
	"forgejo.org/modules/log"
	"forgejo.org/modules/optional"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/util"
	"forgejo.org/modules/web/middleware"
	"forgejo.org/services/auth"
	"forgejo.org/services/auth/source/db"
	"forgejo.org/services/authz"
)

// Ensure the struct implements the interface.
var (
	_ auth.Method = &Basic{}
)

// Basic implements the Auth interface and authenticates requests (API requests
// only) by looking for Basic authentication data or "x-oauth-basic" token in the "Authorization"
// header.
type Basic struct{}

// Verify extracts and validates Basic data (username and password/token) from the
// "Authorization" header of the request and returns the corresponding user object for that
// name/token on successful validation.
// Returns nil if header is empty or validation fails.
func (b *Basic) Verify(req *http.Request, w http.ResponseWriter, _ auth.SessionStore) auth.MethodOutput {
	// Basic authentication should only fire on API, Download or on Git or LFSPaths
	if !middleware.IsAPIPath(req) && !isContainerPath(req) && !isAttachmentDownload(req) && !isGitRawOrAttachOrLFSPath(req) {
		return &auth.AuthenticationNotAttempted{}
	}

	baHead := req.Header.Get("Authorization")
	if len(baHead) == 0 {
		return &auth.AuthenticationNotAttempted{}
	}

	auths := strings.SplitN(baHead, " ", 2)
	if len(auths) != 2 || (strings.ToLower(auths[0]) != "basic") {
		return &auth.AuthenticationNotAttempted{}
	}

	uname, passwd, _ := base.BasicAuthDecode(auths[1])

	// Check if username or password is a token
	isUsernameToken := len(passwd) == 0 || passwd == "x-oauth-basic"
	// Assume username is token
	authToken := uname
	if !isUsernameToken {
		log.Trace("Basic Authorization: Attempting login for: %s", uname)
		// Assume password is token
		authToken = passwd
	} else {
		log.Trace("Basic Authorization: Attempting login with username as token")
	}

	// check oauth2 token
	uid, grantScopes := CheckOAuthAccessToken(req.Context(), authToken)
	if uid != 0 {
		log.Trace("Basic Authorization: Valid OAuthAccessToken for user[%d]", uid)

		u, err := user_model.GetUserByID(req.Context(), uid)
		if err != nil {
			return &auth.AuthenticationError{Error: fmt.Errorf("basic auth GetUserByID: %w", err)}
		}

		var scope auth_model.AccessTokenScope
		if grantScopes != "" {
			scope = auth_model.AccessTokenScope(grantScopes)
		} else {
			scope = auth_model.AccessTokenScopeAll // fallback to all
		}
		return &auth.AuthenticationSuccess{Result: &oAuth2JWTAuthenticationResult{user: u, scope: optional.Some(scope)}}
	}

	// check personal access token
	token, err := auth_model.GetAccessTokenBySHA(req.Context(), authToken)
	if err == nil {
		log.Trace("Basic Authorization: Valid AccessToken for user[%d]", token.UID)
		u, err := user_model.GetUserByID(req.Context(), token.UID)
		if err != nil {
			return &auth.AuthenticationError{Error: fmt.Errorf("basic auth GetUserByID for access token: %w", err)}
		}

		if err = token.UpdateLastUsed(req.Context()); err != nil {
			log.Error("UpdateLastUsed:  %v", err)
		}

		reducer, err := authz.GetAuthorizationReducerForAccessToken(req.Context(), token)
		if err != nil {
			return &auth.AuthenticationError{Error: fmt.Errorf("basic auth GetAuthorizationReducerForAccessToken: %w", err)}
		}

		return &auth.AuthenticationSuccess{Result: &accessTokenAuthenticationResult{user: u, scope: token.Scope, reducer: reducer}}
	} else if !auth_model.IsErrAccessTokenNotExist(err) && !auth_model.IsErrAccessTokenEmpty(err) {
		log.Error("GetAccessTokenBySha: %v", err)
	}

	// check task token
	task, err := actions_model.GetRunningTaskByToken(req.Context(), authToken)
	if err == nil && task != nil {
		log.Trace("Basic Authorization: Valid AccessToken for task[%d]", task.ID)
		return &auth.AuthenticationSuccess{Result: &actionsTaskTokenAuthenticationResult{user: user_model.NewActionsUser(), taskID: task.ID}}
	}

	if !setting.Service.EnableBasicAuth {
		return &auth.AuthenticationAttemptedIncorrectCredential{Error: errors.New("basic authentication by username & password is disabled")}
	}

	log.Trace("Basic Authorization: Attempting SignIn for %s", uname)
	u, source, err := UserSignIn(req.Context(), uname, passwd)
	if err != nil {
		if user_model.IsErrUserNotExist(err) || user_model.IsErrUserProhibitLogin(err) ||
			errors.As(err, &db.ErrUserPasswordInvalid{}) || errors.As(err, &db.ErrUserPasswordNotSet{}) {
			return &auth.AuthenticationAttemptedIncorrectCredential{Error: err}
		}
		return &auth.AuthenticationError{Error: fmt.Errorf("basic auth UserSignIn: %w", err)}
	}

	hashWebAuthn, err := auth_model.HasWebAuthnRegistrationsByUID(req.Context(), u.ID)
	if err != nil {
		return &auth.AuthenticationError{Error: fmt.Errorf("basic auth HasWebAuthnRegistrationsByUID: %w", err)}
	}

	if hashWebAuthn {
		return &auth.AuthenticationAttemptedIncorrectCredential{Error: errors.New("Basic authorization is not allowed while having security keys enrolled")}
	}

	if skipper, ok := source.Cfg.(auth.LocalTwoFASkipper); !ok || !skipper.IsSkipLocalTwoFA() {
		if err := validateTOTP(req, u); err != nil {
			return &auth.AuthenticationAttemptedIncorrectCredential{Error: err}
		}
	}

	log.Trace("Basic Authorization: Logged in user %-v", u)

	return &auth.AuthenticationSuccess{Result: &basicPaswordAuthenticationResult{user: u}}
}

func getOtpHeader(header http.Header) string {
	otpHeader := header.Get("X-Gitea-OTP")
	if forgejoHeader := header.Get("X-Forgejo-OTP"); forgejoHeader != "" {
		otpHeader = forgejoHeader
	}
	return otpHeader
}

func validateTOTP(req *http.Request, u *user_model.User) error {
	twofa, err := auth_model.GetTwoFactorByUID(req.Context(), u.ID)
	if err != nil {
		if auth_model.IsErrTwoFactorNotEnrolled(err) {
			// No 2FA enrollment for this user
			return nil
		}
		return err
	}
	if ok, err := twofa.ValidateTOTP(getOtpHeader(req.Header)); err != nil {
		return err
	} else if !ok {
		return util.NewInvalidArgumentErrorf("invalid provided OTP")
	}
	return nil
}
