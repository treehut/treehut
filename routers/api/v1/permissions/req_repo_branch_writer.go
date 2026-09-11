// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package permissions

import (
	"context"
	"net/http"

	issues_model "forgejo.org/models/issues"
	access_model "forgejo.org/models/perm/access"
	"forgejo.org/models/repo"
	user_model "forgejo.org/models/user"
)

func ReqRepoBranchWriter(ctx Context, branch string) {
	reducer := ctx.Reducer()
	getUserRepoPermission := func(ctx context.Context, repo *repo.Repository, user *user_model.User) (access_model.Permission, error) {
		return access_model.GetUserRepoPermissionWithReducer(ctx, repo, user, reducer)
	}
	if !issues_model.CanMaintainerWriteToBranch(ctx.Context(), *ctx.Permission(), branch, ctx.Doer(), getUserRepoPermission) && !IsUserSiteAdmin(ctx) {
		ctx.Error(http.StatusForbidden, "reqRepoBranchWriter", "user should have a permission to write to this branch")
	}
}
