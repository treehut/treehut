// Copyright 2018 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package context

import (
	"fmt"
	"net/http"
	"slices"

	auth_model "forgejo.org/models/auth"
	"forgejo.org/models/perm"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unit"
	"forgejo.org/modules/log"
)

// RequireRepoAdmin returns a middleware for requiring repository admin permission
func RequireRepoAdmin() func(ctx *Context) {
	return func(ctx *Context) {
		if !ctx.IsSigned || !ctx.Repo.IsAdmin() {
			ctx.NotFound(ctx.Req.URL.RequestURI(), nil)
			return
		}
	}
}

// RequireRepoWriter returns a middleware for requiring repository write to the specify unitType
func RequireRepoWriter(unitType unit.Type) func(ctx *Context) {
	return func(ctx *Context) {
		if !ctx.Repo.CanWrite(unitType) {
			ctx.NotFound(ctx.Req.URL.RequestURI(), nil)
			return
		}
	}
}

// CanEnableEditor checks if the user is allowed to write to the branch of the repo
func CanEnableEditor() func(ctx *Context) {
	return func(ctx *Context) {
		if !ctx.Repo.CanWriteToBranch(ctx, ctx.Doer, ctx.Repo.BranchName) {
			ctx.NotFound("CanWriteToBranch denies permission", nil)
			return
		}
	}
}

// RequireRepoWriterOr returns a middleware for requiring repository write to one of the unit permission
func RequireRepoWriterOr(unitTypes ...unit.Type) func(ctx *Context) {
	return func(ctx *Context) {
		if slices.ContainsFunc(unitTypes, ctx.Repo.CanWrite) {
			return
		}
		ctx.NotFound(ctx.Req.URL.RequestURI(), nil)
	}
}

// RequireRepoReader returns a middleware for requiring repository read to the specify unitType
func RequireRepoReader(unitType unit.Type) func(ctx *Context) {
	return func(ctx *Context) {
		// Typically checks for authentication scopes won't be relevant for non-API requests where this middleware is
		// used; but, some paths like `/user/repo/raw/...` can be accessed with API authentication mechanisms.  In those
		// edge cases, check that `read:repository` scope is present if the authentication method indicates a limited
		// scope.
		hasScope, scope := ctx.Authentication.Scope().Get()
		if hasScope {
			allow, err := scope.HasScope(auth_model.AccessTokenScopeReadRepository)
			if err != nil {
				ctx.ServerError("checking scope failed", err)
				return
			}
			if !allow {
				ctx.Error(http.StatusForbidden, "scopedAccessCheck", fmt.Sprintf("token does not have at least one of required scope(s): %v", auth_model.AccessTokenScopeReadRepository))
				return
			}
		}

		if !ctx.Repo.CanRead(unitType) {
			if log.IsTrace() {
				if ctx.IsSigned {
					log.Trace("Permission Denied: User %-v cannot read %-v in Repo %-v\n"+
						"User in Repo has Permissions: %-+v",
						ctx.Doer,
						unitType,
						ctx.Repo.Repository,
						ctx.Repo.Permission)
				} else {
					log.Trace("Permission Denied: Anonymous user cannot read %-v in Repo %-v\n"+
						"Anonymous user in Repo has Permissions: %-+v",
						unitType,
						ctx.Repo.Repository,
						ctx.Repo.Permission)
				}
			}
			ctx.NotFound(ctx.Req.URL.RequestURI(), nil)
			return
		}
	}
}

// RequireRepoReaderOr returns a middleware for requiring repository write to one of the unit permission
func RequireRepoReaderOr(unitTypes ...unit.Type) func(ctx *Context) {
	return func(ctx *Context) {
		if slices.ContainsFunc(unitTypes, ctx.Repo.CanRead) {
			return
		}
		if log.IsTrace() {
			var format string
			var args []any
			if ctx.IsSigned {
				format = "Permission Denied: User %-v cannot read ["
				args = append(args, ctx.Doer)
			} else {
				format = "Permission Denied: Anonymous user cannot read ["
			}
			for _, unit := range unitTypes {
				format += "%-v, "
				args = append(args, unit)
			}

			format = format[:len(format)-2] + "] in Repo %-v\n" +
				"User in Repo has Permissions: %-+v"
			args = append(args, ctx.Repo.Repository, ctx.Repo.Permission)
			log.Trace(format, args...)
		}
		ctx.NotFound(ctx.Req.URL.RequestURI(), nil)
	}
}

func RequireRepoDelegateActionTrust() func(ctx *Context) {
	return func(ctx *Context) {
		if CheckRepoDelegateActionTrust(ctx) {
			return
		}
		ctx.NotFound(ctx.Req.URL.RequestURI(), nil)
	}
}

func CheckRepoDelegateActionTrust(ctx *Context) bool {
	return ctx.Repo.IsAdmin() || (ctx.IsSigned && ctx.Doer.IsAdmin) || ctx.Repo.CanWrite(unit.TypeActions)
}

// CheckRepoScopedToken check whether personal access token has repo scope
func CheckRepoScopedToken(ctx *Context, repo *repo_model.Repository, level auth_model.AccessTokenScopeLevel) {
	if hasScope, scope := ctx.Authentication.Scope().Get(); hasScope {
		var scopeMatched bool

		requiredScopes := auth_model.GetRequiredScopes(level, auth_model.AccessTokenScopeCategoryRepository)

		// check if scope only applies to public resources
		publicOnly, err := scope.PublicOnly()
		if err != nil {
			ctx.ServerError("HasScope", err)
			return
		}

		if publicOnly && repo.IsPrivate {
			ctx.Error(http.StatusForbidden)
			return
		}

		scopeMatched, err = scope.HasScope(requiredScopes...)
		if err != nil {
			ctx.ServerError("HasScope", err)
			return
		}

		if !scopeMatched {
			ctx.Error(http.StatusForbidden)
			return
		}
	}

	if reducer := ctx.Authentication.Reducer(); reducer != nil {
		var accessMode perm.AccessMode
		switch level {
		case auth_model.Read:
			accessMode = perm.AccessModeRead
		case auth_model.Write:
			accessMode = perm.AccessModeWrite
		case auth_model.NoAccess:
			fallthrough
		default:
			accessMode = perm.AccessModeNone
		}
		actualAccessMode, err := reducer.ReduceRepoAccess(ctx, repo, accessMode)
		if err != nil {
			ctx.ServerError("HasScope", err)
			return
		} else if actualAccessMode != accessMode {
			ctx.Error(http.StatusForbidden)
			return
		}
	}
}

func CheckRuntimeDeterminedScope(ctx *APIContext, scopeCategory auth_model.AccessTokenScopeCategory, level auth_model.AccessTokenScopeLevel, msg string) {
	if hasScope, scope := ctx.Authentication.Scope().Get(); hasScope {
		var scopeMatched bool

		requiredScopes := auth_model.GetRequiredScopes(level, scopeCategory)

		scopeMatched, err := scope.HasScope(requiredScopes...)
		if err != nil {
			ctx.ServerError("HasScope", err)
			return
		}

		if !scopeMatched {
			ctx.Error(http.StatusForbidden, "!scopeMatched", msg)
			return
		}
	}
}
