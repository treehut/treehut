// Copyright 2025 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package actions

import (
	"testing"

	actions_model "forgejo.org/models/actions"
	"forgejo.org/models/db"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unit"
	"forgejo.org/models/unittest"
	webhook_module "forgejo.org/modules/webhook"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServiceActions_startTask(t *testing.T) {
	defer unittest.OverrideFixtures("services/actions/TestServiceActions_startTask")()
	require.NoError(t, unittest.PrepareTestDatabase())

	// Load fixtures that are corrupted and create one valid scheduled workflow
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 4})

	workflowID := "some.yml"
	schedules := []*actions_model.ActionSchedule{
		{
			Title:         "scheduletitle1",
			RepoID:        repo.ID,
			OwnerID:       repo.OwnerID,
			WorkflowID:    workflowID,
			TriggerUserID: repo.OwnerID,
			Ref:           "branch",
			CommitSHA:     "fakeSHA",
			Event:         webhook_module.HookEventSchedule,
			EventPayload:  "fakepayload",
			Specs:         []string{"* * * * *"},
			Content: []byte(
				`
jobs:
  job2:
    runs-on: ubuntu-latest
    steps:
      - run: true
`),
		},
	}

	require.Equal(t, 2, unittest.GetCount(t, actions_model.ActionScheduleSpec{}))
	require.NoError(t, actions_model.CreateScheduleTask(t.Context(), schedules))
	require.Equal(t, 3, unittest.GetCount(t, actions_model.ActionScheduleSpec{}))
	_, err := db.GetEngine(db.DefaultContext).Exec("UPDATE `action_schedule_spec` SET next = 1")
	require.NoError(t, err)

	// After running startTasks an ActionRun row is created for the valid scheduled workflow
	require.Empty(t, unittest.GetCount(t, actions_model.ActionRun{WorkflowID: workflowID}))
	require.NoError(t, startTasks(t.Context()))
	require.NotEmpty(t, unittest.GetCount(t, actions_model.ActionRun{WorkflowID: workflowID}))

	// The invalid workflows loaded from the fixtures are disabled
	repo = unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 4})
	actionUnit, err := repo.GetUnit(t.Context(), unit.TypeActions)
	require.NoError(t, err)
	actionConfig := actionUnit.ActionsConfig()
	assert.True(t, actionConfig.IsWorkflowDisabled("workflow2.yml"))
	assert.True(t, actionConfig.IsWorkflowDisabled("workflow1.yml"))
	assert.False(t, actionConfig.IsWorkflowDisabled("some.yml"))
}

func TestCreateScheduleTask(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 2, OwnerID: 2})

	assertConstant := func(t *testing.T, cron *actions_model.ActionSchedule, run *actions_model.ActionRun) {
		t.Helper()
		assert.Equal(t, cron.Title, run.Title)
		assert.Equal(t, cron.RepoID, run.RepoID)
		assert.Equal(t, cron.OwnerID, run.OwnerID)
		assert.Equal(t, cron.WorkflowID, run.WorkflowID)
		assert.Equal(t, cron.TriggerUserID, run.TriggerUserID)
		assert.Equal(t, cron.Ref, run.Ref)
		assert.Equal(t, cron.CommitSHA, run.CommitSHA)
		assert.Equal(t, cron.Event, run.Event)
		assert.Equal(t, cron.EventPayload, run.EventPayload)
		assert.Equal(t, cron.ID, run.ScheduleID)
		assert.Equal(t, actions_model.StatusWaiting, run.Status)
	}

	assertMutable := func(t *testing.T, expected, run *actions_model.ActionRun) {
		t.Helper()
		assert.Equal(t, expected.NotifyEmail, run.NotifyEmail)
	}

	testCases := []struct {
		name string
		cron actions_model.ActionSchedule
		want []actions_model.ActionRun
	}{
		{
			name: "simple",
			cron: actions_model.ActionSchedule{
				Title:         "scheduletitle1",
				RepoID:        repo.ID,
				OwnerID:       repo.OwnerID,
				WorkflowID:    "some.yml",
				TriggerUserID: repo.OwnerID,
				Ref:           "branch",
				CommitSHA:     "fakeSHA",
				Event:         webhook_module.HookEventSchedule,
				EventPayload:  "fakepayload",
				Content: []byte(
					`
name: test
on: push
jobs:
  job2:
    runs-on: ubuntu-latest
    steps:
      - run: true
`),
			},
			want: []actions_model.ActionRun{
				{
					Title:       "scheduletitle1",
					NotifyEmail: false,
				},
			},
		},
		{
			name: "enable-email-notifications is true",
			cron: actions_model.ActionSchedule{
				Title:         "scheduletitle2",
				RepoID:        repo.ID,
				OwnerID:       repo.OwnerID,
				WorkflowID:    "some.yml",
				TriggerUserID: repo.OwnerID,
				Ref:           "branch",
				CommitSHA:     "fakeSHA",
				Event:         webhook_module.HookEventSchedule,
				EventPayload:  "fakepayload",
				Content: []byte(
					`
name: test
enable-email-notifications: true
on: push
jobs:
  job2:
    runs-on: ubuntu-latest
    steps:
      - run: true
`),
			},
			want: []actions_model.ActionRun{
				{
					Title:       "scheduletitle2",
					NotifyEmail: true,
				},
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			require.NoError(t, CreateScheduleTask(t.Context(), &testCase.cron))
			require.Equal(t, len(testCase.want), unittest.GetCount(t, actions_model.ActionRun{RepoID: repo.ID}))
			for _, expected := range testCase.want {
				run := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{Title: expected.Title})
				assertConstant(t, &testCase.cron, run)
				assertMutable(t, &expected, run)
			}
			unittest.AssertSuccessfulDelete(t, actions_model.ActionRun{RepoID: repo.ID})
		})
	}
}
