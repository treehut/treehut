// Copyright 2025 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package actions

import (
	"testing"

	"forgejo.org/models/unittest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestActionTask_GetTaskByJobAttempt(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	task, err := GetTaskByJobAttempt(t.Context(), 192, 2)
	require.NoError(t, err)
	assert.EqualValues(t, 192, task.JobID)
	assert.EqualValues(t, 2, task.Attempt)

	_, err = GetTaskByJobAttempt(t.Context(), 192, 100)
	assert.ErrorContains(t, err, "task with job_id 192 and attempt 100: resource does not exist")
}

func TestActionTask_CreatePlaceholderTask(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	job := unittest.AssertExistsAndLoadBean(t, &ActionRunJob{ID: 396})
	assert.EqualValues(t, 0, job.TaskID)

	task, err := CreatePlaceholderTask(t.Context(), job, map[string]string{"output1": "value1", "output2": "value2"})
	require.NoError(t, err)

	assert.NotEqualValues(t, 0, task.ID)
	assert.Equal(t, job.ID, task.JobID)
	assert.EqualValues(t, 1, task.Attempt)
	assert.NotEqualValues(t, 0, task.Started)
	assert.NotEqualValues(t, 0, task.Stopped)
	assert.Equal(t, job.Status, task.Status)
	assert.Equal(t, job.RepoID, task.RepoID)
	assert.Equal(t, job.OwnerID, task.OwnerID)
	assert.Equal(t, job.CommitSHA, task.CommitSHA)
	assert.Equal(t, job.IsForkPullRequest, task.IsForkPullRequest)

	taskOutputs, err := FindTaskOutputByTaskID(t.Context(), task.ID)
	require.NoError(t, err)
	require.Len(t, taskOutputs, 2)
	finalOutputs := map[string]string{}
	for _, to := range taskOutputs {
		finalOutputs[to.OutputKey] = to.OutputValue
	}
	assert.Equal(t, map[string]string{"output1": "value1", "output2": "value2"}, finalOutputs)
}

func TestActionTask_GetTasksByRunnerRequestKey(t *testing.T) {
	defer unittest.OverrideFixtures("models/actions/TestActionTask_GetTasksByRunnerRequestKey")()
	require.NoError(t, unittest.PrepareTestDatabase())

	runner := unittest.AssertExistsAndLoadBean(t, &ActionRunner{ID: 12345678})

	// not matching runner_request_key
	tasks, err := GetTasksByRunnerRequestKey(t.Context(), runner, "22288392-2c70-4125-bb01-c7da79fa280c")
	require.NoError(t, err)
	assert.Empty(t, tasks)

	// matching both runner_id and runner_request_key
	tasks, err = GetTasksByRunnerRequestKey(t.Context(), runner, "0a7e017d-4201-4b34-8cf4-de0f431893a4")
	require.NoError(t, err)
	assert.Len(t, tasks, 2)

	// not matching runner_id
	runner = unittest.AssertExistsAndLoadBean(t, &ActionRunner{ID: 10000001})
	tasks, err = GetTasksByRunnerRequestKey(t.Context(), runner, "0a7e017d-4201-4b34-8cf4-de0f431893a4")
	require.NoError(t, err)
	assert.Empty(t, tasks)
}
