// Copyright 2025 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package actions

import (
	"fmt"
	"testing"

	"forgejo.org/models/db"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unittest"
	"forgejo.org/modules/cache"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/test"

	"code.forgejo.org/forgejo/runner/v12/act/jobparser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSetConcurrencyGroup(t *testing.T) {
	run := ActionRun{}
	run.SetConcurrencyGroup("abc123")
	assert.Equal(t, "abc123", run.ConcurrencyGroup)
	run.SetConcurrencyGroup("ABC123") // case should collapse in SetConcurrencyGroup
	assert.Equal(t, "abc123", run.ConcurrencyGroup)
}

func TestSetDefaultConcurrencyGroup(t *testing.T) {
	run := ActionRun{
		Ref:          "refs/heads/main",
		WorkflowID:   "testing",
		TriggerEvent: "pull_request",
	}
	run.SetDefaultConcurrencyGroup()
	assert.Equal(t, "refs/heads/main_testing_pull_request__auto", run.ConcurrencyGroup)
	run = ActionRun{
		Ref:          "refs/heads/main",
		WorkflowID:   "TESTING", // case should collapse in SetDefaultConcurrencyGroup
		TriggerEvent: "pull_request",
	}
	run.SetDefaultConcurrencyGroup()
	assert.Equal(t, "refs/heads/main_testing_pull_request__auto", run.ConcurrencyGroup)
}

func TestGetWorkflowPath(t *testing.T) {
	run := ActionRun{
		WorkflowID:        "ci.yml",
		WorkflowDirectory: ".some/path/to/workflows",
	}
	assert.Equal(t, ".some/path/to/workflows/ci.yml", run.WorkflowPath())
}

func TestGetCommitLink(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	defer test.MockVariableValue(&setting.AppSubURL, "/sub")()

	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})

	run := ActionRun{
		Repo:      repo,
		CommitSHA: "a356d1f1f82945a039cd16d4ce0137bd55284e77",
	}
	assert.Equal(t, "/sub/user2/repo1/commit/a356d1f1f82945a039cd16d4ce0137bd55284e77", run.CommitLink())
}

func TestIsScheduledRun(t *testing.T) {
	scheduledRun := ActionRun{
		CommitSHA:    "a356d1f1f82945a039cd16d4ce0137bd55284e77",
		TriggerEvent: "schedule",
	}
	pushRun := ActionRun{
		CommitSHA:    "8f9b5c6ab342eb11d7422deecef7195b18058b90",
		TriggerEvent: "push",
	}

	assert.True(t, scheduledRun.IsScheduledRun())
	assert.False(t, pushRun.IsScheduledRun())
}

func TestIsManualRun(t *testing.T) {
	manualRunRun := ActionRun{
		CommitSHA:    "a356d1f1f82945a039cd16d4ce0137bd55284e77",
		TriggerEvent: "workflow_dispatch",
	}
	pushRun := ActionRun{
		CommitSHA:    "8f9b5c6ab342eb11d7422deecef7195b18058b90",
		TriggerEvent: "push",
	}

	assert.True(t, manualRunRun.IsDispatchedRun())
	assert.False(t, pushRun.IsDispatchedRun())
}

func TestActionRun_IsValid(t *testing.T) {
	testCases := []struct {
		name    string
		run     ActionRun
		isValid bool
	}{
		{
			name:    "valid run",
			run:     ActionRun{},
			isValid: true,
		},
		{
			name:    "with pre-execution error",
			run:     ActionRun{PreExecutionErrorCode: ErrorCodeIncompleteRunsOnMissingOutput},
			isValid: false,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.isValid, testCase.run.IsValid())
		})
	}
}

func TestActionRun_CanBeRerun(t *testing.T) {
	testCases := []struct {
		name       string
		run        ActionRun
		canBeRerun bool
	}{
		{
			name:       "run with unknown status",
			run:        ActionRun{Status: StatusUnknown},
			canBeRerun: false,
		},
		{
			name:       "successful run",
			run:        ActionRun{Status: StatusSuccess},
			canBeRerun: true,
		},
		{
			name:       "failed run",
			run:        ActionRun{Status: StatusFailure},
			canBeRerun: true,
		},
		{
			name:       "cancelled run",
			run:        ActionRun{Status: StatusCancelled},
			canBeRerun: true,
		},
		{
			name:       "skipped run",
			run:        ActionRun{Status: StatusSkipped},
			canBeRerun: true,
		},
		{
			name:       "waiting run",
			run:        ActionRun{Status: StatusWaiting},
			canBeRerun: false,
		},
		{
			name:       "blocked run",
			run:        ActionRun{Status: StatusBlocked},
			canBeRerun: false,
		},
		{
			name:       "with pre-execution error",
			run:        ActionRun{PreExecutionErrorCode: ErrorCodeIncompleteRunsOnMissingOutput, Status: StatusSuccess},
			canBeRerun: false,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.canBeRerun, testCase.run.CanBeRerun())
		})
	}
}

func TestRepoNumOpenActions(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	err := cache.Init()
	require.NoError(t, err)

	t.Run("Repo 1", func(t *testing.T) {
		repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
		clearRepoRunCountCache(t.Context(), repo)
		assert.Equal(t, 0, RepoNumOpenActions(t.Context(), repo.ID))
	})

	t.Run("Repo 4", func(t *testing.T) {
		repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 4})
		clearRepoRunCountCache(t.Context(), repo)
		assert.Equal(t, 0, RepoNumOpenActions(t.Context(), repo.ID))
	})

	t.Run("Repo 63", func(t *testing.T) {
		repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 63})
		clearRepoRunCountCache(t.Context(), repo)
		assert.Equal(t, 1, RepoNumOpenActions(t.Context(), repo.ID))
	})

	t.Run("Cache Invalidation", func(t *testing.T) {
		repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 63})
		assert.Equal(t, 1, RepoNumOpenActions(t.Context(), repo.ID))

		err = db.DeleteBeans(t.Context(), &ActionRun{RepoID: repo.ID})
		require.NoError(t, err)

		// Even though we've deleted ActionRun, expecting that the number of open runs is still 1 (cached)
		assert.Equal(t, 1, RepoNumOpenActions(t.Context(), repo.ID))

		// Now that we clear the cache, computation should be performed
		clearRepoRunCountCache(t.Context(), repo)
		assert.Equal(t, 0, RepoNumOpenActions(t.Context(), repo.ID))
	})
}

func TestActionRun_GetRunsNotDoneByRepoIDAndPullRequestPosterID(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	repoID := int64(10)
	pullRequestID := int64(3)
	pullRequestPosterID := int64(30)

	runDone := &ActionRun{
		RepoID:              repoID,
		PullRequestID:       pullRequestID,
		PullRequestPosterID: pullRequestPosterID,
		Status:              StatusSuccess,
	}
	require.NoError(t, InsertRun(t.Context(), runDone, nil))

	unrelatedUser := int64(5)
	runNotByPoster := &ActionRun{
		RepoID:              repoID,
		PullRequestID:       pullRequestID,
		PullRequestPosterID: unrelatedUser,
		Status:              StatusRunning,
	}
	require.NoError(t, InsertRun(t.Context(), runNotByPoster, nil))

	unrelatedRepository := int64(6)
	runNotInTheSameRepository := &ActionRun{
		RepoID:              unrelatedRepository,
		PullRequestID:       pullRequestID,
		PullRequestPosterID: pullRequestPosterID,
		Status:              StatusSuccess,
	}
	require.NoError(t, InsertRun(t.Context(), runNotInTheSameRepository, nil))

	for _, status := range []Status{StatusUnknown, StatusWaiting, StatusRunning} {
		t.Run(fmt.Sprintf("%s", status), func(t *testing.T) {
			runNotDone := &ActionRun{
				RepoID:              repoID,
				PullRequestID:       pullRequestID,
				Status:              status,
				PullRequestPosterID: pullRequestPosterID,
			}
			require.NoError(t, InsertRun(t.Context(), runNotDone, nil))
			runs, err := GetRunsNotDoneByRepoIDAndPullRequestPosterID(t.Context(), repoID, pullRequestPosterID)
			require.NoError(t, err)
			require.Len(t, runs, 1)
			run := runs[0]
			assert.Equal(t, runNotDone.ID, run.ID)
			assert.Equal(t, runNotDone.Status, run.Status)
			unittest.AssertSuccessfulDelete(t, run)
		})
	}
}

func TestActionRun_NeedApproval(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	pullRequestPosterID := int64(4)
	repoID := int64(10)
	pullRequestID := int64(2)
	runDoesNotNeedApproval := &ActionRun{
		RepoID:              repoID,
		PullRequestID:       pullRequestID,
		PullRequestPosterID: pullRequestPosterID,
	}
	require.NoError(t, InsertRun(t.Context(), runDoesNotNeedApproval, nil))
	unrelatedRepository := int64(6)
	runNotInTheSameRepository := &ActionRun{
		RepoID:              unrelatedRepository,
		PullRequestID:       pullRequestID,
		PullRequestPosterID: pullRequestPosterID,
		NeedApproval:        true,
	}
	require.NoError(t, InsertRun(t.Context(), runNotInTheSameRepository, nil))
	unrelatedPullRequest := int64(3)
	runNotInTheSamePullRequest := &ActionRun{
		RepoID:              repoID,
		PullRequestID:       unrelatedPullRequest,
		PullRequestPosterID: pullRequestPosterID,
		NeedApproval:        true,
	}
	require.NoError(t, InsertRun(t.Context(), runNotInTheSamePullRequest, nil))

	t.Run("HasRunThatNeedApproval is false", func(t *testing.T) {
		has, err := HasRunThatNeedApproval(t.Context(), repoID, pullRequestID)
		require.NoError(t, err)
		assert.False(t, has)
	})

	runNeedApproval := &ActionRun{
		RepoID:              repoID,
		PullRequestID:       pullRequestID,
		PullRequestPosterID: pullRequestPosterID,
		NeedApproval:        true,
	}
	require.NoError(t, InsertRun(t.Context(), runNeedApproval, nil))

	t.Run("HasRunThatNeedApproval is true", func(t *testing.T) {
		has, err := HasRunThatNeedApproval(t.Context(), repoID, pullRequestID)
		require.NoError(t, err)
		assert.True(t, has)
	})

	assertApprovalEqual := func(t *testing.T, expected, actual *ActionRun) {
		t.Helper()
		assert.Equal(t, expected.RepoID, actual.RepoID)
		assert.Equal(t, expected.PullRequestID, actual.PullRequestID)
		assert.Equal(t, expected.PullRequestPosterID, actual.PullRequestPosterID)
		assert.Equal(t, expected.NeedApproval, actual.NeedApproval)
	}

	t.Run("GetRunsThatNeedApproval", func(t *testing.T) {
		runs, err := GetRunsThatNeedApprovalByRepoIDAndPullRequestID(t.Context(), repoID, pullRequestID)
		require.NoError(t, err)
		require.Len(t, runs, 1)
		assertApprovalEqual(t, runNeedApproval, runs[0])
	})
}

func TestActionRun_IncompleteMatrix(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	pullRequestPosterID := int64(4)
	repoID := int64(10)
	pullRequestID := int64(2)
	runDoesNotNeedApproval := &ActionRun{
		RepoID:              repoID,
		PullRequestID:       pullRequestID,
		PullRequestPosterID: pullRequestPosterID,
	}

	workflowRaw := []byte(`
jobs:
  job2:
    runs-on: ubuntu-latest
    strategy:
      matrix:
        dim1: "${{ fromJSON(needs.other-job.outputs.some-output) }}"
    steps:
      - run: true
`)
	workflows, err := jobparser.Parse(workflowRaw, false, jobparser.WithJobOutputs(map[string]map[string]string{}))
	require.NoError(t, err)
	require.True(t, workflows[0].IncompleteMatrix) // must be set for this test scenario to be valid

	require.NoError(t, InsertRun(t.Context(), runDoesNotNeedApproval, workflows))

	jobs, err := db.Find[ActionRunJob](t.Context(), FindRunJobOptions{RunID: runDoesNotNeedApproval.ID})
	require.NoError(t, err)
	require.Len(t, jobs, 1)
	job := jobs[0]

	// Expect job with an incomplete matrix to be StatusBlocked:
	assert.Equal(t, StatusBlocked, job.Status)
}

func TestActionRun_IncompleteRunsOn(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	pullRequestPosterID := int64(4)
	repoID := int64(10)
	pullRequestID := int64(2)
	runDoesNotNeedApproval := &ActionRun{
		RepoID:              repoID,
		PullRequestID:       pullRequestID,
		PullRequestPosterID: pullRequestPosterID,
	}

	workflowRaw := []byte(`
jobs:
  job2:
    runs-on: ${{ needs.other-job.outputs.some-output }}
    steps:
      - run: true
`)
	workflows, err := jobparser.Parse(workflowRaw, false, jobparser.WithJobOutputs(map[string]map[string]string{}), jobparser.SupportIncompleteRunsOn())
	require.NoError(t, err)
	require.True(t, workflows[0].IncompleteRunsOn) // must be set for this test scenario to be valid

	require.NoError(t, InsertRun(t.Context(), runDoesNotNeedApproval, workflows))

	jobs, err := db.Find[ActionRunJob](t.Context(), FindRunJobOptions{RunID: runDoesNotNeedApproval.ID})
	require.NoError(t, err)
	require.Len(t, jobs, 1)
	job := jobs[0]

	// Expect job with an incomplete runs-on to be StatusBlocked:
	assert.Equal(t, StatusBlocked, job.Status)
}

func TestActionRun_FindOuterWorkflowCall(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	pullRequestPosterID := int64(4)
	repoID := int64(10)
	pullRequestID := int64(2)
	run := &ActionRun{
		RepoID:              repoID,
		PullRequestID:       pullRequestID,
		PullRequestPosterID: pullRequestPosterID,
	}

	workflowRaw := []byte(`
jobs:
  outer-job:
    uses: ./.forgejo/workflows/reusable.yml
`)
	workflows, err := jobparser.Parse(workflowRaw, false,
		jobparser.WithJobOutputs(map[string]map[string]string{}),
		jobparser.ExpandLocalReusableWorkflows(func(job *jobparser.Job, path string) ([]byte, error) {
			return []byte(`
on:
  workflow_call:
jobs:
  inner-job-1:
    runs-on: debian
    steps: []
  inner-job-2:
    runs-on: debian
    steps: []
`), nil
		}))
	require.NoError(t, err)
	require.NoError(t, InsertRun(t.Context(), run, workflows))

	jobs, err := db.Find[ActionRunJob](t.Context(), FindRunJobOptions{RunID: run.ID})
	require.NoError(t, err)
	require.Len(t, jobs, 3)

	for _, j := range jobs {
		t.Run(j.Name, func(t *testing.T) {
			_, err := j.DecodeWorkflowPayload()
			require.NoError(t, err)
			outer, err := run.FindOuterWorkflowCall(t.Context(), j)
			if j.Name == "outer-job" {
				require.ErrorContains(t, err, "invalid state for FindOuterWorkflowCall")
			} else {
				require.NoError(t, err)
				require.NotNil(t, outer)
				assert.Equal(t, "outer-job", outer.Name)
			}
		})
	}
}

func TestActionRun_IncompleteWith(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	pullRequestPosterID := int64(4)
	repoID := int64(10)
	pullRequestID := int64(2)
	runDoesNotNeedApproval := &ActionRun{
		RepoID:              repoID,
		PullRequestID:       pullRequestID,
		PullRequestPosterID: pullRequestPosterID,
	}

	workflowRaw := []byte(`
jobs:
  outer-job:
    with:
      some_input: ${{ needs.other-job.outputs.some-output }}
    uses: ./.forgejo/workflows/reusable.yml
`)
	workflows, err := jobparser.Parse(workflowRaw, false,
		jobparser.WithJobOutputs(map[string]map[string]string{}),
		jobparser.ExpandLocalReusableWorkflows(func(job *jobparser.Job, path string) ([]byte, error) {
			return []byte(`
on:
  workflow_call:
    inputs:
      some_input:
        type: string
jobs:
  inner-job:
    runs-on: debian
    steps: []
`), nil
		}))
	require.NoError(t, err)
	require.True(t, workflows[0].IncompleteWith) // must be set for this test scenario to be valid

	require.NoError(t, InsertRun(t.Context(), runDoesNotNeedApproval, workflows))

	jobs, err := db.Find[ActionRunJob](t.Context(), FindRunJobOptions{RunID: runDoesNotNeedApproval.ID})
	require.NoError(t, err)
	require.Len(t, jobs, 1)
	job := jobs[0]

	// Expect job with an incomplete with to be StatusBlocked:
	assert.Equal(t, StatusBlocked, job.Status)
}

func TestComputeRunStatus(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	t.Run("no changes", func(t *testing.T) {
		run, columns, err := ComputeRunStatus(t.Context(), 791)
		require.NoError(t, err)
		assert.Equal(t, StatusSuccess, run.Status)
		assert.NotContains(t, columns, "status")
		assert.EqualValues(t, 1683636528, run.Started)
		assert.NotContains(t, columns, "started")
		assert.EqualValues(t, 1683636626, run.Stopped)
		assert.NotContains(t, columns, "stopped")
	})

	t.Run("change status", func(t *testing.T) {
		job := unittest.AssertExistsAndLoadBean(t, &ActionRunJob{ID: 192})
		job.Status = StatusFailure
		affected, err := db.GetEngine(t.Context()).Cols("status").ID(job.ID).Update(job)
		require.NoError(t, err)
		require.EqualValues(t, 1, affected)

		run, columns, err := ComputeRunStatus(t.Context(), 791)
		require.NoError(t, err)
		assert.Equal(t, StatusFailure, run.Status)
		assert.Contains(t, columns, "status")
		assert.NotContains(t, columns, "started")
		assert.NotContains(t, columns, "stopped")
	})

	t.Run("won't change started if not running", func(t *testing.T) {
		job := unittest.AssertExistsAndLoadBean(t, &ActionRunJob{ID: 192})
		job.Status = StatusBlocked
		affected, err := db.GetEngine(t.Context()).Cols("status").ID(job.ID).Update(job)
		require.NoError(t, err)
		require.EqualValues(t, 1, affected)

		preRun := unittest.AssertExistsAndLoadBean(t, &ActionRun{ID: 791})
		preRun.Started = 0
		affected, err = db.GetEngine(t.Context()).Cols("started").ID(preRun.ID).Update(preRun)
		require.NoError(t, err)
		require.EqualValues(t, 1, affected)

		run, columns, err := ComputeRunStatus(t.Context(), 791)
		require.NoError(t, err)
		assert.Equal(t, StatusBlocked, run.Status)
		assert.EqualValues(t, 0, run.Started)
		assert.Contains(t, columns, "status")
		assert.NotContains(t, columns, "started")
		assert.NotContains(t, columns, "stopped")
	})

	t.Run("change started", func(t *testing.T) {
		// Need the job to be "Running" for started to appear to change
		job := unittest.AssertExistsAndLoadBean(t, &ActionRunJob{ID: 192})
		job.Status = StatusRunning
		affected, err := db.GetEngine(t.Context()).Cols("status").ID(job.ID).Update(job)
		require.NoError(t, err)
		require.EqualValues(t, 1, affected)

		preRun := unittest.AssertExistsAndLoadBean(t, &ActionRun{ID: 791})
		preRun.Started = 0
		affected, err = db.GetEngine(t.Context()).Cols("started").ID(preRun.ID).Update(preRun)
		require.NoError(t, err)
		require.EqualValues(t, 1, affected)

		run, columns, err := ComputeRunStatus(t.Context(), 791)
		require.NoError(t, err)
		assert.Equal(t, StatusRunning, run.Status)
		assert.NotEqualValues(t, 0, run.Started)
		assert.Contains(t, columns, "status")
		assert.Contains(t, columns, "started")
		assert.NotContains(t, columns, "stopped")
	})

	t.Run("won't change stopped if not done", func(t *testing.T) {
		job := unittest.AssertExistsAndLoadBean(t, &ActionRunJob{ID: 192})
		job.Status = StatusRunning
		affected, err := db.GetEngine(t.Context()).Cols("status").ID(job.ID).Update(job)
		require.NoError(t, err)
		require.EqualValues(t, 1, affected)

		preRun := unittest.AssertExistsAndLoadBean(t, &ActionRun{ID: 791})
		preRun.Stopped = 0
		affected, err = db.GetEngine(t.Context()).Cols("stopped").ID(preRun.ID).Update(preRun)
		require.NoError(t, err)
		require.EqualValues(t, 1, affected)

		run, columns, err := ComputeRunStatus(t.Context(), 791)
		require.NoError(t, err)
		assert.Equal(t, StatusRunning, run.Status)
		assert.EqualValues(t, 0, run.Stopped)
		assert.Contains(t, columns, "status")
		assert.NotContains(t, columns, "stopped")
	})

	t.Run("change stopped", func(t *testing.T) {
		// Need the job to be some version of Done for stopped to appear to change
		job := unittest.AssertExistsAndLoadBean(t, &ActionRunJob{ID: 192})
		job.Status = StatusSuccess
		affected, err := db.GetEngine(t.Context()).Cols("status").ID(job.ID).Update(job)
		require.NoError(t, err)
		require.EqualValues(t, 1, affected)

		preRun := unittest.AssertExistsAndLoadBean(t, &ActionRun{ID: 791})
		preRun.Stopped = 0
		affected, err = db.GetEngine(t.Context()).Cols("stopped").ID(preRun.ID).Update(preRun)
		require.NoError(t, err)
		require.EqualValues(t, 1, affected)

		run, columns, err := ComputeRunStatus(t.Context(), 791)
		require.NoError(t, err)
		assert.Equal(t, StatusSuccess, run.Status)
		assert.NotEqualValues(t, 0, run.Stopped)
		assert.NotContains(t, columns, "status")
		assert.NotContains(t, columns, "started")
		assert.Contains(t, columns, "stopped")
	})
}

func TestInsertRunJobs(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	pullRequestPosterID := int64(4)
	repoID := int64(10)
	pullRequestID := int64(2)
	actionRun := &ActionRun{
		RepoID:              repoID,
		PullRequestID:       pullRequestID,
		PullRequestPosterID: pullRequestPosterID,
		CommitSHA:           "1421f75bc5474c69fdb1dc176bcb96d381f935dd",
	}

	workflowRaw := []byte(`
jobs:
  build:
    runs-on: fedora
  test:
    runs-on: debian
    steps: []
`)
	jobs, err := jobparser.Parse(workflowRaw, false)
	require.NoError(t, err)

	require.NoError(t, InsertRun(t.Context(), actionRun, jobs))

	insertedJobs, err := db.Find[ActionRunJob](t.Context(), FindRunJobOptions{RunID: actionRun.ID})
	require.NoError(t, err)
	require.Len(t, insertedJobs, 2)

	assert.Equal(t, actionRun.ID, insertedJobs[0].RunID)
	assert.Equal(t, actionRun.RepoID, insertedJobs[0].RepoID)
	assert.Equal(t, actionRun.OwnerID, insertedJobs[0].OwnerID)
	assert.Equal(t, actionRun.CommitSHA, insertedJobs[0].CommitSHA)
	assert.Equal(t, actionRun.IsForkPullRequest, insertedJobs[0].IsForkPullRequest)
	assert.Equal(t, "build", insertedJobs[0].Name)
	assert.Equal(t, "build", insertedJobs[0].JobID)
	assert.Empty(t, insertedJobs[0].Needs)
	assert.Equal(t, []string{"fedora"}, insertedJobs[0].RunsOn)
	assert.Equal(t, int64(1), insertedJobs[0].Attempt)
	assert.Zero(t, insertedJobs[0].Started)
	assert.Zero(t, insertedJobs[0].Stopped)
	assert.Zero(t, insertedJobs[0].TaskID)
	assert.Equal(t, StatusWaiting, insertedJobs[0].Status)

	assert.Equal(t, actionRun.ID, insertedJobs[1].RunID)
	assert.Equal(t, actionRun.RepoID, insertedJobs[1].RepoID)
	assert.Equal(t, actionRun.OwnerID, insertedJobs[1].OwnerID)
	assert.Equal(t, actionRun.CommitSHA, insertedJobs[1].CommitSHA)
	assert.Equal(t, actionRun.IsForkPullRequest, insertedJobs[1].IsForkPullRequest)
	assert.Equal(t, "test", insertedJobs[1].Name)
	assert.Equal(t, "test", insertedJobs[1].JobID)
	assert.Empty(t, insertedJobs[1].Needs)
	assert.Equal(t, []string{"debian"}, insertedJobs[1].RunsOn)
	assert.Equal(t, int64(1), insertedJobs[1].Attempt)
	assert.Zero(t, insertedJobs[1].Started)
	assert.Zero(t, insertedJobs[1].Stopped)
	assert.Zero(t, insertedJobs[1].TaskID)
	assert.Equal(t, StatusWaiting, insertedJobs[1].Status)
}

func TestActionRunLoadAttributes(t *testing.T) {
	run := &ActionRun{
		RepoID:        10,
		TriggerUserID: 1000,
	}
	require.NoError(t, run.LoadAttributes(t.Context()))
	assert.Equal(t, "ghost", run.TriggerUser.LowerName)
}
