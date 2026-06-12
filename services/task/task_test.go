// Copyright 2025 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package task

import (
	"testing"

	admin_model "forgejo.org/models/admin"
	issues_model "forgejo.org/models/issues"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/migration"
	"forgejo.org/modules/queue"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/structs"
	"forgejo.org/modules/test"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateMigrateTask(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})

	t.Run("Transaction failure", func(t *testing.T) {
		defer unittest.SetFaultInjector(2)()

		task, err := CreateMigrateTask(t.Context(), user, user, migration.MigrateOptions{
			CloneAddr:    "https://admin:password2@example.com",
			AuthPassword: "password",
			AuthToken:    "token",
			RepoName:     "migrate-test-2",
		})
		require.ErrorIs(t, err, unittest.ErrFaultInjected)
		require.Nil(t, task)

		unittest.AssertExistsIf(t, false, &admin_model.Task{})
	})

	t.Run("Normal", func(t *testing.T) {
		task, err := CreateMigrateTask(t.Context(), user, user, migration.MigrateOptions{
			CloneAddr:    "https://admin:password@example.com",
			AuthPassword: "password",
			AuthToken:    "token",
			RepoName:     "migrate-test",
		})
		require.NoError(t, err)
		require.NotNil(t, task)

		config, err := task.MigrateConfig()
		require.NoError(t, err)
		require.NotNil(t, config)

		assert.Equal(t, "token", config.AuthToken)
		assert.Equal(t, "password", config.AuthPassword)
		assert.Equal(t, "https://admin:password@example.com", config.CloneAddr)
	})
}

func TestRetryMigrateTask(t *testing.T) {
	defer unittest.OverrideFixtures("services/task/fixtures/TestRetryMigrateTask/")()
	require.NoError(t, unittest.PrepareTestDatabase())

	t.Run("Migrate task does not exist", func(t *testing.T) {
		err := RetryMigrateTask(t.Context(), 100)
		require.ErrorIs(t, err, admin_model.ErrTaskDoesNotExist{RepoID: 100})
	})

	t.Run("Normal", func(t *testing.T) {
		// Override the task queue temporarily.
		called := false
		testQueue, err := queue.NewWorkerPoolQueueWithContext(t.Context(), "task", setting.QueueSettings{Type: "immediate"}, func(items ...*admin_model.Task) []*admin_model.Task {
			if assert.Len(t, items, 1) {
				assert.Empty(t, items[0].Message)
				assert.Equal(t, structs.TaskStatusQueued, items[0].Status)
				assert.EqualValues(t, 1002, items[0].ID)
			}
			called = true
			return nil
		}, true)
		require.NoError(t, err)
		defer test.MockVariableValue(&taskQueue, testQueue)()

		// Preconditions.
		unittest.AssertExistsIf(t, true, &repo_model.Repository{ID: 20})
		unittest.AssertExistsIf(t, true, &admin_model.Task{RepoID: 20, Status: structs.TaskStatusFailed})
		unittest.AssertExistsIf(t, true, &issues_model.Issue{RepoID: 20})
		unittest.AssertCount(t, &repo_model.RepoUnit{RepoID: 20}, 5)

		require.NoError(t, RetryMigrateTask(t.Context(), 20))

		// Verify queue was called.
		assert.True(t, called)
		// Verify some beans were NOT deleted.
		unittest.AssertExistsIf(t, true, &repo_model.Repository{ID: 20})
		unittest.AssertExistsIf(t, true, &admin_model.Task{RepoID: 20, Status: structs.TaskStatusQueued})
		unittest.AssertCount(t, &repo_model.RepoUnit{RepoID: 20}, 5)
		// Verify some beans were deleted.
		unittest.AssertExistsIf(t, false, &issues_model.Issue{RepoID: 20})
	})
}
