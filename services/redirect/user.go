// Copyright 2025 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT
package redirect

import (
	"context"

	user_model "forgejo.org/models/user"
)

// LookupUserRedirect returns the userID if there's a redirect registered for the
// username. It additionally checks if the doer has permission to view the new
// user.
func LookupUserRedirect(ctx context.Context, doer *user_model.User, userName string) (int64, error) {
	redirect, err := user_model.GetUserRedirect(ctx, userName)
	if err != nil {
		return 0, err
	}

	redirectUser, err := user_model.GetUserByID(ctx, redirect.RedirectUserID)
	if err != nil {
		return 0, err
	}

	if !user_model.IsUserVisibleToViewer(ctx, redirectUser, doer) {
		return 0, user_model.ErrUserRedirectNotExist{Name: userName, MissingPermission: true}
	}

	return redirect.RedirectUserID, nil
}
