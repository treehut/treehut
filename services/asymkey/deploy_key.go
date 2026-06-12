// Copyright 2021 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package asymkey

import (
	"context"

	"forgejo.org/models"
	asymkey_model "forgejo.org/models/asymkey"
	"forgejo.org/models/db"
)

// DeleteDeployKey deletes deploy key from its repository authorized_keys file if needed.
func DeleteDeployKey(ctx context.Context, id, repoID int64) error {
	if err := db.WithTx(ctx, func(ctx context.Context) error {
		return models.DeleteDeployKey(ctx, id, repoID)
	}); err != nil {
		return err
	}

	return asymkey_model.RewriteAllPublicKeys(ctx)
}
