// Copyright 2016 The Gogs Authors. All rights reserved.
// Copyright 2020 The Gitea Authors.
// Copyright 2024 The Forgejo Authors.
// SPDX-License-Identifier: MIT

package user

import (
	"net/http"

	"forgejo.org/models/db"
	access_model "forgejo.org/models/perm/access"
	repo_model "forgejo.org/models/repo"
	user_model "forgejo.org/models/user"
	api "forgejo.org/modules/structs"
	"forgejo.org/routers/api/v1/utils"
	"forgejo.org/services/context"
	"forgejo.org/services/convert"
	"forgejo.org/services/repository"
)

// getStarredRepos returns the repos that the user with the specified userID has
// starred
func getStarredRepos(ctx *context.APIContext, user *user_model.User, listOptions db.ListOptions) ([]*api.Repository, int64, error) {
	starredRepos, total, err := repo_model.SearchRepository(ctx, &repo_model.SearchRepoOptions{
		ListOptions:          listOptions,
		Actor:                ctx.Doer(),
		StarredByID:          user.ID,
		Private:              ctx.IsSigned(),
		AuthorizationReducer: ctx.Reducer(),
	})
	if err != nil {
		return nil, 0, err
	}

	repos := make([]*api.Repository, len(starredRepos))
	for i, starred := range starredRepos {
		// Resource filtering is implemented above in the call to GetStarredRepos, and doesn't need to be taken into
		// account here:
		//
		// nosemgrep: forgejo-api-use-resource-GetUserRepoPermission
		permission, err := access_model.GetUserRepoPermission(ctx, starred, ctx.Doer())
		if err != nil {
			return nil, 0, err
		}
		repos[i] = convert.ToRepo(ctx, starred, permission)
	}
	return repos, total, nil
}

// GetStarredRepos returns the repos that the given user has starred
func GetStarredRepos(ctx *context.APIContext) {
	// swagger:operation GET /users/{username}/starred user userListStarred
	// ---
	// summary: The repos that the given user has starred
	// produces:
	// - application/json
	// parameters:
	// - name: username
	//   in: path
	//   description: username of user
	//   type: string
	//   required: true
	// - name: page
	//   in: query
	//   description: page number of results to return (1-based)
	//   type: integer
	// - name: limit
	//   in: query
	//   description: page size of results
	//   type: integer
	// responses:
	//   "200":
	//     "$ref": "#/responses/RepositoryList"
	//   "404":
	//     "$ref": "#/responses/notFound"

	repos, total, err := getStarredRepos(ctx, ctx.User(), utils.GetListOptions(ctx))
	if err != nil {
		ctx.Error(http.StatusInternalServerError, "getStarredRepos", err)
		return
	}

	ctx.SetTotalCountHeader(total)
	ctx.JSON(http.StatusOK, &repos)
}

// GetMyStarredRepos returns the repos that the authenticated user has starred
func GetMyStarredRepos(ctx *context.APIContext) {
	// swagger:operation GET /user/starred user userCurrentListStarred
	// ---
	// summary: The repos that the authenticated user has starred
	// parameters:
	// - name: page
	//   in: query
	//   description: page number of results to return (1-based)
	//   type: integer
	// - name: limit
	//   in: query
	//   description: page size of results
	//   type: integer
	// produces:
	// - application/json
	// responses:
	//   "200":
	//     "$ref": "#/responses/RepositoryList"
	//   "401":
	//     "$ref": "#/responses/unauthorized"
	//   "403":
	//     "$ref": "#/responses/forbidden"

	repos, total, err := getStarredRepos(ctx, ctx.Doer(), utils.GetListOptions(ctx))
	if err != nil {
		ctx.Error(http.StatusInternalServerError, "getStarredRepos", err)
		return
	}

	ctx.SetTotalCountHeader(total)
	ctx.JSON(http.StatusOK, &repos)
}

// IsStarring returns whether the authenticated is starring the repo
func IsStarring(ctx *context.APIContext) {
	// swagger:operation GET /user/starred/{owner}/{repo} user userCurrentCheckStarring
	// ---
	// summary: Whether the authenticated is starring the repo
	// parameters:
	// - name: owner
	//   in: path
	//   description: owner of the repo
	//   type: string
	//   required: true
	// - name: repo
	//   in: path
	//   description: name of the repo
	//   type: string
	//   required: true
	// responses:
	//   "204":
	//     "$ref": "#/responses/empty"
	//   "401":
	//     "$ref": "#/responses/unauthorized"
	//   "403":
	//     "$ref": "#/responses/forbidden"
	//   "404":
	//     "$ref": "#/responses/notFound"

	if repo_model.IsStaring(ctx, ctx.Doer().ID, ctx.Repo().Repository.ID) {
		ctx.Status(http.StatusNoContent)
	} else {
		ctx.NotFound()
	}
}

// Star the repo specified in the APIContext, as the authenticated user
func Star(ctx *context.APIContext) {
	// swagger:operation PUT /user/starred/{owner}/{repo} user userCurrentPutStar
	// ---
	// summary: Star the given repo
	// parameters:
	// - name: owner
	//   in: path
	//   description: owner of the repo to star
	//   type: string
	//   required: true
	// - name: repo
	//   in: path
	//   description: name of the repo to star
	//   type: string
	//   required: true
	// responses:
	//   "204":
	//     "$ref": "#/responses/empty"
	//   "401":
	//     "$ref": "#/responses/unauthorized"
	//   "403":
	//     "$ref": "#/responses/forbidden"
	//   "404":
	//     "$ref": "#/responses/notFound"

	err := repository.StarRepoAndSendLikeActivities(ctx, *ctx.Doer(), ctx.Repo().Repository.ID, true)
	if err != nil {
		ctx.Error(http.StatusInternalServerError, "StarRepo", err)
		return
	}

	ctx.Status(http.StatusNoContent)
}

// Unstar the repo specified in the APIContext, as the authenticated user
func Unstar(ctx *context.APIContext) {
	// swagger:operation DELETE /user/starred/{owner}/{repo} user userCurrentDeleteStar
	// ---
	// summary: Unstar the given repo
	// parameters:
	// - name: owner
	//   in: path
	//   description: owner of the repo to unstar
	//   type: string
	//   required: true
	// - name: repo
	//   in: path
	//   description: name of the repo to unstar
	//   type: string
	//   required: true
	// responses:
	//   "204":
	//     "$ref": "#/responses/empty"
	//   "401":
	//     "$ref": "#/responses/unauthorized"
	//   "403":
	//     "$ref": "#/responses/forbidden"
	//   "404":
	//     "$ref": "#/responses/notFound"

	err := repository.StarRepoAndSendLikeActivities(ctx, *ctx.Doer(), ctx.Repo().Repository.ID, false)
	if err != nil {
		ctx.Error(http.StatusInternalServerError, "StarRepo", err)
		return
	}
	ctx.Status(http.StatusNoContent)
}
