// Copyright 2022 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package common

import (
	"forgejo.org/modules/web/middleware"
	auth_service "forgejo.org/services/auth"
	"forgejo.org/services/context"
)

func AuthShared(ctx *context.Base, sessionStore auth_service.SessionStore, authMethod auth_service.Method) auth_service.MethodOutput {
	output := authMethod.Verify(ctx.Req, ctx.Resp, sessionStore)

	var ar auth_service.AuthenticationResult
	switch v := output.(type) {
	case *auth_service.AuthenticationSuccess:
		ar = v.Result
	case *auth_service.AuthenticationNotAttempted:
		ar = &auth_service.UnauthenticatedResult{}
	}
	if ar != nil {
		doer := ar.User()
		if doer != nil {
			if ctx.Locale.Language() != doer.Language {
				ctx.Locale = middleware.Locale(ctx.Resp, ctx.Req)
			}
			ctx.Data["IsSigned"] = true
			ctx.Data[middleware.ContextDataKeySignedUser] = doer
			ctx.Data["SignedUserID"] = doer.ID
			ctx.Data["IsAdmin"] = doer.IsAdmin
		} else {
			ctx.Data["IsSigned"] = false
			ctx.Data["SignedUserID"] = int64(0)
		}
	}
	return output
}

// VerifyOptions contains required or check options
type VerifyOptions struct {
	SignInRequired  bool
	SignOutRequired bool
	AdminRequired   bool
	DisableCSRF     bool
}
