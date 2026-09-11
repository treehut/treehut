// Copyright 2022 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package repo

import (
	"context"
	"fmt"

	"forgejo.org/models/db"
	"forgejo.org/models/perm"
	"forgejo.org/models/unit"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/timeutil"

	"xorm.io/builder"
)

// Collaboration represent the relation between an individual and a repository.
type Collaboration struct {
	ID          int64              `xorm:"pk autoincr"`
	RepoID      int64              `xorm:"UNIQUE(s) INDEX NOT NULL REFERENCES(repository, id)"`
	UserID      int64              `xorm:"UNIQUE(s) INDEX NOT NULL REFERENCES(user, id)"`
	Mode        perm.AccessMode    `xorm:"DEFAULT 2 NOT NULL"`
	CreatedUnix timeutil.TimeStamp `xorm:"INDEX created"`
	UpdatedUnix timeutil.TimeStamp `xorm:"INDEX updated"`
}

func init() {
	db.RegisterModel(new(Collaboration))
}

// Collaborator represents a user with collaboration details.
type Collaborator struct {
	*user_model.User
	Collaboration *Collaboration
}

// GetCollaborators returns the collaborators for a repository
func GetCollaborators(ctx context.Context, repoID int64, listOptions db.ListOptions) ([]*Collaborator, error) {
	collaborations, err := db.Find[Collaboration](ctx, FindCollaborationOptions{
		ListOptions: listOptions,
		RepoID:      repoID,
	})
	if err != nil {
		return nil, fmt.Errorf("db.Find[Collaboration]: %w", err)
	}

	collaborators := make([]*Collaborator, 0, len(collaborations))
	userIDs := make([]int64, 0, len(collaborations))
	for _, c := range collaborations {
		userIDs = append(userIDs, c.UserID)
	}

	usersMap := make(map[int64]*user_model.User)
	if err := db.GetEngine(ctx).In("id", userIDs).Find(&usersMap); err != nil {
		return nil, fmt.Errorf("Find users map by user ids: %w", err)
	}

	for _, c := range collaborations {
		u := usersMap[c.UserID]
		if u == nil {
			u = user_model.NewGhostUser()
		}
		collaborators = append(collaborators, &Collaborator{
			User:          u,
			Collaboration: c,
		})
	}
	return collaborators, nil
}

// IsCollaborator check if a user is a collaborator of a repository
func IsCollaborator(ctx context.Context, repoID, userID int64) (bool, error) {
	return db.GetEngine(ctx).Get(&Collaboration{RepoID: repoID, UserID: userID})
}

type FindCollaborationOptions struct {
	db.ListOptions
	RepoID int64
}

func (opts FindCollaborationOptions) ToConds() builder.Cond {
	return builder.And(builder.Eq{"repo_id": opts.RepoID})
}

// GetCollaboratorWithUser returns all collaborator IDs of collabUserID on
// repositories of ownerID.
func GetCollaboratorWithUser(ctx context.Context, ownerID, collabUserID int64) ([]int64, error) {
	collabsID := make([]int64, 0, 8)
	err := db.GetEngine(ctx).Table("collaboration").Select("collaboration.`id`").
		Join("INNER", "repository", "repository.id = collaboration.repo_id").
		Where("repository.`owner_id` = ?", ownerID).
		And("collaboration.`user_id` = ?", collabUserID).
		Find(&collabsID)

	return collabsID, err
}

// IsOwnerMemberCollaborator checks if a provided user is the owner, a collaborator or a member of a team in a repository
func IsOwnerMemberCollaborator(ctx context.Context, repo *Repository, userID int64) (bool, error) {
	if repo.OwnerID == userID {
		return true, nil
	}
	teamMember, err := db.GetEngine(ctx).Join("INNER", "team_repo", "team_repo.team_id = team_user.team_id").
		Join("INNER", "team_unit", "team_unit.team_id = team_user.team_id").
		Where("team_repo.repo_id = ?", repo.ID).
		And("team_unit.`type` = ?", unit.TypeCode).
		And("team_user.uid = ?", userID).Table("team_user").Exist()
	if err != nil {
		return false, err
	}
	if teamMember {
		return true, nil
	}

	return db.GetEngine(ctx).Get(&Collaboration{RepoID: repo.ID, UserID: userID})
}
