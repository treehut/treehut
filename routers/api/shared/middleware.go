// Copyright 2024 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package shared

import (
	"errors"
	"fmt"
	"net/http"

	auth_model "forgejo.org/models/auth"
	"forgejo.org/modules/log"
	"forgejo.org/modules/setting"
	"forgejo.org/routers/common"
	"forgejo.org/services/auth"
	auth_method "forgejo.org/services/auth/method"
	"forgejo.org/services/authz"
	"forgejo.org/services/context"

	"github.com/go-chi/cors"
)

func Middlewares() (stack []any) {
	stack = append(stack, securityHeaders())

	if setting.CORSConfig.Enabled {
		stack = append(stack, cors.Handler(cors.Options{
			AllowedOrigins:   setting.CORSConfig.AllowDomain,
			AllowedMethods:   setting.CORSConfig.Methods,
			AllowCredentials: setting.CORSConfig.AllowCredentials,
			AllowedHeaders:   append([]string{"Authorization", "X-Gitea-OTP", "X-Forgejo-OTP"}, setting.CORSConfig.Headers...),
			MaxAge:           int(setting.CORSConfig.MaxAge.Seconds()),
		}))
	}
	return append(stack,
		context.APIContexter(),

		checkDeprecatedAuthMethods,
		// Get user from session if logged in.
		apiAuthentication(buildAuthGroup()),
		apiAuthorization,
		verifyAuthWithOptions(&common.VerifyOptions{
			SignInRequired: setting.Service.RequireSignInView,
		}),
	)
}

func buildAuthGroup() *auth_method.Group {
	group := auth_method.NewGroup(
		&auth_method.OAuth2{},
		&auth_method.HTTPSign{},
		&auth_method.Basic{}, // FIXME: this should be removed once we don't allow basic auth in API
	)
	if setting.Service.EnableReverseProxyAuthAPI {
		group.Add(&auth_method.ReverseProxy{})
	}

	return group
}

func apiAuthentication(authMethod auth.Method) func(*context.APIContext) {
	return func(ctx *context.APIContext) {
		output := common.AuthShared(ctx.Base, nil, authMethod)
		var ar auth.AuthenticationResult
		switch v := output.(type) {
		case *auth.AuthenticationSuccess:
			ar = v.Result
		case *auth.AuthenticationNotAttempted:
			ar = &auth.UnauthenticatedResult{}
		case *auth.AuthenticationAttemptedIncorrectCredential:
			ctx.Error(http.StatusUnauthorized, "APIAuth", v.Error)
			return
		case *auth.AuthenticationError:
			ctx.ServerError("authentication error", v.Error)
			return
		default:
			ctx.ServerError("authentication error", errors.New("unexpected result from common.AuthShared"))
			return
		}
		if ar == nil {
			ctx.ServerError("nil authentication result", errors.New("nil authentication result"))
			return
		}
		ctx.Doer = ar.User()
		ctx.IsSigned = ctx.Doer != nil
		ctx.Authentication = ar
	}
}

func apiAuthorization(ctx *context.APIContext) {
	if hasScope, scope := ctx.Authentication.Scope().Get(); hasScope {
		publicOnly, err := scope.PublicOnly()
		if err != nil {
			ctx.Error(http.StatusForbidden, "tokenRequiresScope", "parsing public resource scope failed: "+err.Error())
			return
		}
		ctx.PublicOnly = publicOnly
	}

	reducer := ctx.Authentication.Reducer()
	if reducer != nil {
		ctx.Reducer = reducer
	} else {
		// No Reducer will be populated if the auth method wasn't an PAT.  In this case, we populate `ctx.Reducer` so no
		// nil checks are needed, and we respect the scope `PublicOnly()` so that it it's safe to just rely on
		// `ctx.Reducer` to account for public-only access:
		if ctx.PublicOnly {
			ctx.Reducer = &authz.PublicReposAuthorizationReducer{}
		} else {
			ctx.Reducer = &authz.AllAccessAuthorizationReducer{}
		}
	}
}

// verifyAuthWithOptions checks authentication according to options
func verifyAuthWithOptions(options *common.VerifyOptions) func(ctx *context.APIContext) {
	return func(ctx *context.APIContext) {
		// Check prohibit login users.
		if ctx.IsSigned {
			if !ctx.Doer.IsActive && setting.Service.RegisterEmailConfirm {
				ctx.Data["Title"] = ctx.Tr("auth.active_your_account")
				ctx.JSON(http.StatusForbidden, map[string]string{
					"message": "This account is not activated.",
				})
				return
			}
			if !ctx.Doer.IsActive || ctx.Doer.ProhibitLogin {
				log.Info("Failed authentication attempt for %s from %s", ctx.Doer.Name, ctx.RemoteAddr())
				ctx.Data["Title"] = ctx.Tr("auth.prohibit_login")
				ctx.JSON(http.StatusForbidden, map[string]string{
					"message": "This account is prohibited from signing in, please contact your site administrator.",
				})
				return
			}

			if ctx.Doer.MustChangePassword {
				ctx.JSON(http.StatusForbidden, map[string]string{
					"message": "You must change your password. Change it at: " + setting.AppURL + "/user/change_password",
				})
				return
			}

			if ctx.Doer.MustHaveTwoFactor() {
				hasTwoFactor, err := auth_model.HasTwoFactorByUID(ctx, ctx.Doer.ID)
				if err != nil {
					ctx.Data["Title"] = ctx.Tr("auth.prohibit_login")
					log.Error("Error getting 2fa: %s", err)
					ctx.JSON(http.StatusInternalServerError, map[string]string{
						"message": fmt.Sprintf("Error getting 2fa: %s", err),
					})
					return
				}
				if !hasTwoFactor {
					ctx.Data["Title"] = ctx.Tr("auth.prohibit_login")
					ctx.JSON(http.StatusForbidden, map[string]string{
						"message": ctx.Locale.TrString("error.must_enable_2fa", fmt.Sprintf("%suser/settings/security", setting.AppURL)),
					})
					return
				}
			}
		}

		// Redirect to dashboard if user tries to visit any non-login page.
		if options.SignOutRequired && ctx.IsSigned && ctx.Req.URL.RequestURI() != "/" {
			ctx.Redirect(setting.AppSubURL + "/")
			return
		}

		if options.SignInRequired {
			if !ctx.IsSigned {
				// Restrict API calls with error message.
				ctx.JSON(http.StatusForbidden, map[string]string{
					"message": "Only signed in user is allowed to call APIs.",
				})
				return
			} else if !ctx.Doer.IsActive && setting.Service.RegisterEmailConfirm {
				ctx.Data["Title"] = ctx.Tr("auth.active_your_account")
				ctx.JSON(http.StatusForbidden, map[string]string{
					"message": "This account is not activated.",
				})
				return
			}
		}

		if options.AdminRequired {
			if !ctx.IsUserSiteAdmin() {
				ctx.JSON(http.StatusForbidden, map[string]string{
					"message": "You have no permission to request for this.",
				})
				return
			}
		}
	}
}

// check for and warn against deprecated authentication options
func checkDeprecatedAuthMethods(ctx *context.APIContext) {
	if ctx.FormString("token") != "" || ctx.FormString("access_token") != "" {
		ctx.Resp.Header().Set("Warning", "token and access_token API authentication is deprecated and will be removed in Forgejo v13.0.0. Please use AuthorizationHeaderToken instead. Existing queries will continue to work but without authorization.")
	}
}

func securityHeaders() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(resp http.ResponseWriter, req *http.Request) {
			// CORB: https://www.chromium.org/Home/chromium-security/corb-for-developers
			// http://stackoverflow.com/a/3146618/244009
			resp.Header().Set("x-content-type-options", "nosniff")
			next.ServeHTTP(resp, req)
		})
	}
}
