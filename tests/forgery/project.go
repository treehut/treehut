// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package forgery

import (
	"testing"

	org_model "forgejo.org/models/organization"
	project_model "forgejo.org/models/project"
	repo_model "forgejo.org/models/repo"
	user_model "forgejo.org/models/user"

	"github.com/stretchr/testify/require"
)

type CreateProjectOptions struct {
	TemplateType project_model.TemplateType
	CardType     project_model.CardType
}

func CreateProject[T org_model.Organization | user_model.User | repo_model.Repository](t testing.TB, owner *T, opts *CreateProjectOptions) *project_model.Project {
	t.Helper()

	if opts == nil {
		opts = &CreateProjectOptions{}
	}

	p := &project_model.Project{
		Title:        t.Name(),
		Description:  "Test project",
		TemplateType: opts.TemplateType,
		CardType:     opts.CardType,
	}
	switch o := any(owner).(type) {
	case *org_model.Organization:
		p.Owner = o.AsUser()
		p.OwnerID = o.ID
		p.Type = project_model.TypeOrganization
	case *user_model.User:
		p.Owner = o
		p.OwnerID = o.ID
		p.Type = project_model.TypeIndividual
	case *repo_model.Repository:
		p.Repo = o
		p.RepoID = o.ID
		p.Type = project_model.TypeRepository
	default:
		t.Fatalf("unexpected owner type %T", o)
	}
	err := project_model.NewProject(t.Context(), p)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = project_model.DeleteProjectByID(t.Context(), p.ID)
	})
	return p
}
