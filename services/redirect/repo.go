// Copyright 2025 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT
package redirect

import (
	"context"

	access_model "forgejo.org/models/perm/access"
	repo_model "forgejo.org/models/repo"
	user_model "forgejo.org/models/user"
)

// LookupRepoRedirect returns the repository ID if there's a redirect registered for
// the ownerID repository name pair. It checks if the doer has permission to view
// the new repository.
func LookupRepoRedirect(ctx context.Context, doer *user_model.User, ownerID int64, repoName string) (int64, error) {
	redirectID, err := repo_model.GetRedirect(ctx, ownerID, repoName)
	if err != nil {
		return 0, err
	}

	redirectRepo, err := repo_model.GetRepositoryByID(ctx, redirectID)
	if err != nil {
		return 0, err
	}

	perm, err := access_model.GetUserRepoPermission(ctx, redirectRepo, doer)
	if err != nil {
		return 0, err
	}

	if !perm.HasAccess() {
		return 0, repo_model.ErrRedirectNotExist{OwnerID: ownerID, RepoName: repoName, MissingPermission: true}
	}

	return redirectID, nil
}
