// Copyright 2025 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"context"
	"net/url"
	"testing"

	actions_model "forgejo.org/models/actions"
	org_model "forgejo.org/models/organization"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/util"
	actions_service "forgejo.org/services/actions"
	org_service "forgejo.org/services/org"
	repo_service "forgejo.org/services/repository"
	user_service "forgejo.org/services/user"

	runnerv1 "code.forgejo.org/forgejo/actions-proto/runner/v1"
	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test that the ephemeral runner is deleted when the task is finished
func TestEphemeralRunnerDeletionByTaskCompletion(t *testing.T) {
	if !setting.Database.Type.IsSQLite3() {
		t.Skip()
	}

	defer unittest.OverrideFixtures("tests/integration/fixtures/TestEphemeralRunner")()

	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		// Verify runner exists before the test
		runner, err := actions_model.GetRunnerByID(context.Background(), 10000008)
		require.NoError(t, err)
		require.NotNil(t, runner)
		require.True(t, runner.Ephemeral, "runner should be ephemeral")

		// Verify task exists and is running
		task := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionTask{ID: 10054})
		assert.Equal(t, actions_model.StatusRunning, task.Status)
		assert.Equal(t, int64(10000008), task.RunnerID)

		// Token can be found in models/fixtures/action_runner.yml with id: 10000008
		runnerClient := newMockRunnerClient(
			runner.UUID,
			"mysuuupersecrettoekn",
		)

		// Finish the Task
		resp, err := runnerClient.runnerServiceClient.UpdateTask(
			context.Background(),
			connect.NewRequest(&runnerv1.UpdateTaskRequest{
				State: &runnerv1.TaskState{
					Id:     task.ID,
					Result: runnerv1.Result_RESULT_SUCCESS,
				},
			}),
		)
		require.NoError(t, err)
		require.NotNil(t, resp)
		assert.Equal(t, runnerv1.Result_RESULT_SUCCESS, resp.Msg.State.Result)

		// Expect the ephemeral runner has been deleted
		_, err = actions_model.GetRunnerByID(context.Background(), 10000008)
		assert.ErrorIs(t, err, util.ErrNotExist, "ephemeral runner should be deleted after task completion")
	})
}

func TestEphemeralRunnerDeletedByTaskZombieCleanup(t *testing.T) {
	if !setting.Database.Type.IsSQLite3() {
		t.Skip()
	}

	defer unittest.OverrideFixtures("tests/integration/fixtures/TestEphemeralRunner")()

	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		// Verify runner exists before the test
		runner, err := actions_model.GetRunnerByID(context.Background(), 10000011)
		require.NoError(t, err)
		require.NotNil(t, runner)
		require.True(t, runner.Ephemeral, "runner should be ephemeral")

		// Verify zombie task exists and is running
		task := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionTask{ID: 10055})
		assert.Equal(t, actions_model.StatusRunning, task.Status)
		assert.Equal(t, int64(10000011), task.RunnerID)

		// Run zombie task cleanup
		err = actions_service.StopZombieTasks(context.Background())
		require.NoError(t, err)

		// Expect the ephemeral runner has been deleted
		_, err = actions_model.GetRunnerByID(context.Background(), 10000011)
		assert.ErrorIs(t, err, util.ErrNotExist, "ephemeral runner should be deleted after zombie task cleanup")
	})
}

func TestEphemeralRunnerDeletionOnRepositoryDeletion(t *testing.T) {
	if !setting.Database.Type.IsSQLite3() {
		t.Skip()
	}

	defer unittest.OverrideFixtures("tests/integration/fixtures/TestEphemeralRunner")()

	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		runner, err := actions_model.GetRunnerByID(t.Context(), 10000008)
		require.NoError(t, err)
		assert.Equal(t, int64(0), runner.OwnerID, "runner should not start in user scope")
		assert.NotEqual(t, int64(0), runner.RepoID, "runner should start in repo scope")

		task := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionTask{ID: 10054})
		assert.Equal(t, actions_model.StatusRunning, task.Status)

		err = repo_service.DeleteRepositoryDirectly(t.Context(), task.RepoID, repo_service.DeleteRepositoryOpts{IgnoreOrgTeams: true})
		require.NoError(t, err)

		_, err = actions_model.GetRunnerByID(t.Context(), 10000008)
		assert.ErrorIs(t, err, util.ErrNotExist)
	})
}

// Test that the ephemeral runner is deleted when a user is deleted
func TestEphemeralRunnerDeletionOnUserDeletion(t *testing.T) {
	if !setting.Database.Type.IsSQLite3() {
		t.Skip()
	}

	defer unittest.OverrideFixtures("tests/integration/fixtures/TestEphemeralRunner")()

	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		runner, err := actions_model.GetRunnerByID(t.Context(), 10000012)
		require.NoError(t, err)
		assert.NotEqual(t, int64(0), runner.OwnerID, "runner should start in user scope")
		assert.Equal(t, int64(0), runner.RepoID, "runner should not start in repo scope")

		user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
		err = user_service.DeleteUser(t.Context(), user, true)
		require.NoError(t, err)

		unittest.AssertNotExistsBean(t, runner)
	})
}

// Test that the ephemeral runner is deleted when an organization is deleted
func TestEphemeralRunnerDeletionOnOrgDeletion(t *testing.T) {
	if !setting.Database.Type.IsSQLite3() {
		t.Skip()
	}

	defer unittest.OverrideFixtures("tests/integration/fixtures/TestEphemeralRunner")()

	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		runner, err := actions_model.GetRunnerByID(t.Context(), 10000013)
		require.NoError(t, err)
		assert.NotEqual(t, int64(0), runner.OwnerID, "runner should start in org scope")
		assert.Equal(t, int64(0), runner.RepoID, "runner should not start in repo scope")

		org := unittest.AssertExistsAndLoadBean(t, &org_model.Organization{ID: runner.OwnerID})
		err = org_service.DeleteOrganization(t.Context(), org, true)
		require.NoError(t, err)

		unittest.AssertNotExistsBean(t, runner)
	})
}
