// SPDX-License-Identifier: MIT

package actions

import (
	"fmt"
	"testing"

	"forgejo.org/models/db"
	"forgejo.org/models/unittest"
	"forgejo.org/modules/container"
	"forgejo.org/modules/timeutil"

	"code.forgejo.org/forgejo/runner/v12/act/jobparser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestActionRunJob_ItRunsOn(t *testing.T) {
	actionJob := ActionRunJob{RunsOn: []string{"ubuntu"}}
	agentLabels := []string{"ubuntu", "node-20"}

	assert.True(t, actionJob.ItRunsOn(agentLabels))
	assert.False(t, actionJob.ItRunsOn([]string{}))

	actionJob.RunsOn = append(actionJob.RunsOn, "node-20")

	assert.True(t, actionJob.ItRunsOn(agentLabels))

	agentLabels = []string{"ubuntu"}

	assert.False(t, actionJob.ItRunsOn(agentLabels))

	actionJob.RunsOn = []string{}

	assert.False(t, actionJob.ItRunsOn(agentLabels))
}

func TestActionRunJob_HTMLURL(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	tests := []struct {
		id       int64
		expected string
	}{
		{
			id:       192,
			expected: "https://try.gitea.io/user5/repo4/actions/runs/187/jobs/0/attempt/3",
		},
		{
			id:       393,
			expected: "https://try.gitea.io/user2/repo1/actions/runs/187/jobs/1/attempt/1",
		},
		{
			id:       394,
			expected: "https://try.gitea.io/user2/repo1/actions/runs/187/jobs/2/attempt/2",
		},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("id=%d", tt.id), func(t *testing.T) {
			var job ActionRunJob
			has, err := db.GetEngine(t.Context()).Where("id=?", tt.id).Get(&job)
			require.NoError(t, err)
			require.True(t, has, "load ActionRunJob from fixture")

			err = job.LoadAttributes(t.Context())
			require.NoError(t, err)

			url, err := job.HTMLURL(t.Context())
			require.NoError(t, err)
			assert.Equal(t, tt.expected, url)
		})
	}
}

func TestActionRunJob_HasIncompleteMatrix(t *testing.T) {
	tests := []struct {
		name         string
		job          ActionRunJob
		isIncomplete bool
		needs        *jobparser.IncompleteNeeds
		errContains  string
	}{
		{
			name:         "normal workflow",
			job:          ActionRunJob{WorkflowPayload: []byte("name: workflow")},
			isIncomplete: false,
		},
		{
			name:         "incomplete_matrix workflow",
			job:          ActionRunJob{WorkflowPayload: []byte("name: workflow\nincomplete_matrix: true\nincomplete_matrix_needs: { job: abc }")},
			needs:        &jobparser.IncompleteNeeds{Job: "abc"},
			isIncomplete: true,
		},
		{
			name:        "unparseable workflow",
			job:         ActionRunJob{WorkflowPayload: []byte("name: []\nincomplete_matrix: true")},
			errContains: "failure unmarshaling WorkflowPayload to SingleWorkflow: yaml: unmarshal errors",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isIncomplete, needs, err := tt.job.HasIncompleteMatrix()
			if tt.errContains != "" {
				assert.ErrorContains(t, err, tt.errContains)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.isIncomplete, isIncomplete)
				assert.Equal(t, tt.needs, needs)
			}
		})
	}
}

func TestActionRunJob_HasIncompleteRunsOn(t *testing.T) {
	tests := []struct {
		name         string
		job          ActionRunJob
		isIncomplete bool
		needs        *jobparser.IncompleteNeeds
		matrix       *jobparser.IncompleteMatrix
		errContains  string
	}{
		{
			name:         "normal workflow",
			job:          ActionRunJob{WorkflowPayload: []byte("name: workflow")},
			isIncomplete: false,
		},
		{
			name:         "nincomplete_runs_on workflow",
			job:          ActionRunJob{WorkflowPayload: []byte("name: workflow\nincomplete_runs_on: true\nincomplete_runs_on_needs: { job: abc }")},
			needs:        &jobparser.IncompleteNeeds{Job: "abc"},
			isIncomplete: true,
		},
		{
			name:         "nincomplete_runs_on workflow",
			job:          ActionRunJob{WorkflowPayload: []byte("name: workflow\nincomplete_runs_on: true\nincomplete_runs_on_matrix: { dimension: abc }")},
			matrix:       &jobparser.IncompleteMatrix{Dimension: "abc"},
			isIncomplete: true,
		},
		{
			name:        "unparseable workflow",
			job:         ActionRunJob{WorkflowPayload: []byte("name: []\nincomplete_runs_on: true")},
			errContains: "failure unmarshaling WorkflowPayload to SingleWorkflow: yaml: unmarshal errors",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isIncomplete, needs, matrix, err := tt.job.HasIncompleteRunsOn()
			if tt.errContains != "" {
				assert.ErrorContains(t, err, tt.errContains)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.isIncomplete, isIncomplete)
				assert.Equal(t, tt.needs, needs)
				assert.Equal(t, tt.matrix, matrix)
			}
		})
	}
}

func TestActionRunJob_IsWorkflowCallOuterJob(t *testing.T) {
	tests := []struct {
		name                   string
		job                    ActionRunJob
		isWorkflowCallOuterJob bool
		errContains            string
	}{
		{
			name:                   "normal workflow",
			job:                    ActionRunJob{WorkflowPayload: []byte("name: workflow")},
			isWorkflowCallOuterJob: false,
		},
		{
			name:                   "workflow_call outer job",
			job:                    ActionRunJob{WorkflowPayload: []byte("name: test\njobs:\n  job:\n    if: false\n__metadata:\n  workflow_call_id: b5a9f46f1f2513d7777fde50b169d323a6519e349cc175484c947ac315a209ed\n")},
			isWorkflowCallOuterJob: true,
		},
		{
			name:        "unparseable workflow",
			job:         ActionRunJob{WorkflowPayload: []byte("name: []\nincomplete_runs_on: true")},
			errContains: "failure unmarshaling WorkflowPayload to SingleWorkflow: yaml: unmarshal errors",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isWorkflowCallOuterJob, err := tt.job.IsWorkflowCallOuterJob()
			if tt.errContains != "" {
				assert.ErrorContains(t, err, tt.errContains)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.isWorkflowCallOuterJob, isWorkflowCallOuterJob)
			}
		})
	}
}

func TestActionRunJob_IsWorkflowCallInnerJob(t *testing.T) {
	tests := []struct {
		name                   string
		job                    ActionRunJob
		isWorkflowCallInnerJob bool
		errContains            string
	}{
		{
			name:                   "normal workflow",
			job:                    ActionRunJob{WorkflowPayload: []byte("on: [workflow_dispatch]\nname: workflow")},
			isWorkflowCallInnerJob: false,
		},
		{
			name:                   "inner job",
			job:                    ActionRunJob{WorkflowPayload: []byte("on:\n  workflow_call:\nname: workflow\n__metadata:\n  workflow_call_parent: b5a9f46f1f2513d7777fde50b169d323a6519e349cc175484c947ac315a209ed\n")},
			isWorkflowCallInnerJob: true,
		},
		{
			name:        "unparseable workflow",
			job:         ActionRunJob{WorkflowPayload: []byte("name: []\nincomplete_runs_on: true")},
			errContains: "failure unmarshaling WorkflowPayload to SingleWorkflow: yaml: unmarshal errors",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isWorkflowCallInnerJob, err := tt.job.IsWorkflowCallInnerJob()
			if tt.errContains != "" {
				assert.ErrorContains(t, err, tt.errContains)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.isWorkflowCallInnerJob, isWorkflowCallInnerJob)
			}
		})
	}
}

func TestActionRunJob_HasIncompleteWith(t *testing.T) {
	tests := []struct {
		name         string
		job          ActionRunJob
		isIncomplete bool
		needs        *jobparser.IncompleteNeeds
		matrix       *jobparser.IncompleteMatrix
		errContains  string
	}{
		{
			name:         "normal workflow",
			job:          ActionRunJob{WorkflowPayload: []byte("name: workflow")},
			isIncomplete: false,
		},
		{
			name:         "incomplete_with workflow",
			job:          ActionRunJob{WorkflowPayload: []byte("name: workflow\nincomplete_with: true\nincomplete_with_needs: { job: abc }")},
			needs:        &jobparser.IncompleteNeeds{Job: "abc"},
			isIncomplete: true,
		},
		{
			name:         "incomplete_with workflow",
			job:          ActionRunJob{WorkflowPayload: []byte("name: workflow\nincomplete_with: true\nincomplete_with_matrix: { dimension: abc }")},
			matrix:       &jobparser.IncompleteMatrix{Dimension: "abc"},
			isIncomplete: true,
		},
		{
			name:        "unparseable workflow",
			job:         ActionRunJob{WorkflowPayload: []byte("name: []\nincomplete_with: true")},
			errContains: "failure unmarshaling WorkflowPayload to SingleWorkflow: yaml: unmarshal errors",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isIncomplete, needs, matrix, err := tt.job.HasIncompleteWith()
			if tt.errContains != "" {
				assert.ErrorContains(t, err, tt.errContains)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.isIncomplete, isIncomplete)
				assert.Equal(t, tt.needs, needs)
				assert.Equal(t, tt.matrix, matrix)
			}
		})
	}
}

func TestRunHasOtherJobs(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	jobs, err := GetRunJobsByRunID(t.Context(), 791)
	require.NoError(t, err)
	assert.Len(t, jobs, 1)

	has, err := RunHasOtherJobs(t.Context(), 791, nil)
	require.NoError(t, err)
	assert.True(t, has)

	has, err = RunHasOtherJobs(t.Context(), 791, []*ActionRunJob{})
	require.NoError(t, err)
	assert.True(t, has)

	has, err = RunHasOtherJobs(t.Context(), 791, jobs)
	require.NoError(t, err)
	assert.False(t, has)
}

func TestActionRunJobPrepareNextAttempt(t *testing.T) {
	lastHandle := "original-handle"
	job := ActionRunJob{ID: 46, Handle: lastHandle}

	err := job.PrepareNextAttempt(StatusWaiting)
	require.NoError(t, err)

	assert.NotEqual(t, lastHandle, job.Handle)
	assert.NotEmpty(t, job.Handle)
	assert.Equal(t, int64(1), job.Attempt)
	assert.Zero(t, job.Started)
	assert.Zero(t, job.Stopped)
	assert.Zero(t, job.TaskID)
	assert.Equal(t, StatusWaiting, job.Status)

	lastHandle = job.Handle
	job.Started = timeutil.TimeStampNow()
	job.Stopped = timeutil.TimeStampNow()
	job.TaskID = int64(59)
	job.Status = StatusFailure

	err = job.PrepareNextAttempt(StatusBlocked)
	require.NoError(t, err)

	assert.NotEqual(t, lastHandle, job.Handle)
	assert.NotEmpty(t, job.Handle)
	assert.Equal(t, int64(2), job.Attempt)
	assert.Zero(t, job.Started)
	assert.Zero(t, job.Stopped)
	assert.Zero(t, job.TaskID)
	assert.Equal(t, StatusBlocked, job.Status)

	lastHandle = job.Handle

	// The job hasn't finished yet. Preparing a next attempt should not be possible. It should be left untouched.
	err = job.PrepareNextAttempt(StatusWaiting)
	require.ErrorContains(t, err, "cannot prepare next attempt because job 46 is active: blocked")

	assert.Equal(t, lastHandle, job.Handle)
	assert.Equal(t, int64(2), job.Attempt)
	assert.Zero(t, job.Started)
	assert.Zero(t, job.Stopped)
	assert.Zero(t, job.TaskID)
	assert.Equal(t, StatusBlocked, job.Status)
}

func TestIsRequestedByRunner(t *testing.T) {
	sameHandle := "4a1ca0be-4470-486d-8504-89b4a5ac00cf"
	differentHandle := "88423da3-67af-4f2d-9a92-a0db822697e9"
	emptyHandle := ""

	job := &ActionRunJob{ID: 422, Attempt: 5, Handle: sameHandle}

	assert.True(t, job.IsRequestedByRunner(nil))
	assert.True(t, job.IsRequestedByRunner(&sameHandle))
	assert.False(t, job.IsRequestedByRunner(&differentHandle))
	assert.False(t, job.IsRequestedByRunner(&emptyHandle))

	// Old jobs that were created before the introduction of Handle do not have one.
	emptyHandleJob := &ActionRunJob{ID: 422, Attempt: 5, Handle: ""}

	assert.True(t, emptyHandleJob.IsRequestedByRunner(nil))
	assert.True(t, emptyHandleJob.IsRequestedByRunner(&emptyHandle))

	assert.False(t, emptyHandleJob.IsRequestedByRunner(&differentHandle))
}

func TestAllNeedsExist(t *testing.T) {
	testCases := []struct {
		name               string
		job                ActionRunJob
		existingJobIDs     container.Set[string]
		expectedUnknownIDs []string
		ok                 bool
	}{
		{
			name:               "no needs",
			job:                ActionRunJob{Needs: nil},
			existingJobIDs:     container.Set[string]{},
			expectedUnknownIDs: []string{},
			ok:                 true,
		},
		{
			name:               "empty needs",
			job:                ActionRunJob{Needs: []string{}},
			existingJobIDs:     container.Set[string]{},
			expectedUnknownIDs: []string{},
			ok:                 true,
		},
		{
			name:               "satisfied needs",
			job:                ActionRunJob{Needs: []string{"job1", "job2"}},
			existingJobIDs:     container.SetOf("job2", "job1"),
			expectedUnknownIDs: []string{},
			ok:                 true,
		},
		{
			name:               "unsatisfied needs",
			job:                ActionRunJob{Needs: []string{"unknown", "job2"}},
			existingJobIDs:     container.SetOf("job2", "job1"),
			expectedUnknownIDs: []string{"unknown"},
			ok:                 false,
		},
		{
			name:               "comparison is case-sensitive",
			job:                ActionRunJob{Needs: []string{"Job1", "job2"}},
			existingJobIDs:     container.SetOf("job2", "job1"),
			expectedUnknownIDs: []string{"Job1"},
			ok:                 false,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			unknownIDs, ok := testCase.job.AllNeedsExist(testCase.existingJobIDs)

			assert.Equal(t, testCase.ok, ok)
			assert.Equal(t, testCase.expectedUnknownIDs, unknownIDs)
		})
	}
}

func TestActionRunJob_CanBeRerun(t *testing.T) {
	testCases := []struct {
		name          string
		job           ActionRunJob
		canBeRerun    bool
		expectedError string
	}{
		{
			name:       "job with unknown status",
			job:        ActionRunJob{Run: &ActionRun{Status: StatusSuccess}, Status: StatusUnknown},
			canBeRerun: false,
		},
		{
			name:       "successful job",
			job:        ActionRunJob{Run: &ActionRun{Status: StatusSuccess}, Status: StatusSuccess},
			canBeRerun: true,
		},
		{
			name:       "failed job",
			job:        ActionRunJob{Run: &ActionRun{Status: StatusSuccess}, Status: StatusFailure},
			canBeRerun: true,
		},
		{
			name:       "cancelled job",
			job:        ActionRunJob{Run: &ActionRun{Status: StatusSuccess}, Status: StatusCancelled},
			canBeRerun: true,
		},
		{
			name:       "skipped job",
			job:        ActionRunJob{Run: &ActionRun{Status: StatusSuccess}, Status: StatusSkipped},
			canBeRerun: true,
		},
		{
			name:       "waiting job",
			job:        ActionRunJob{Run: &ActionRun{Status: StatusSuccess}, Status: StatusWaiting},
			canBeRerun: false,
		},
		{
			name:       "blocked job",
			job:        ActionRunJob{Run: &ActionRun{Status: StatusSuccess}, Status: StatusBlocked},
			canBeRerun: false,
		},
		{
			name:          "ActionRun is nil",
			job:           ActionRunJob{ID: 12, Run: nil, Status: StatusSuccess},
			expectedError: "cannot load run 0 of job 12",
		},
		{
			name:       "with busy run but completed job",
			job:        ActionRunJob{Run: &ActionRun{Status: StatusRunning}, Status: StatusSuccess},
			canBeRerun: true,
		},
		{
			name: "with run that cannot be run",
			job: ActionRunJob{
				Run:    &ActionRun{Status: StatusRunning, PreExecutionErrorCode: ErrorCodeEventDetectionError},
				Status: StatusSuccess,
			},
			canBeRerun: false,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			result, err := testCase.job.CanBeRerun(t.Context())

			if testCase.expectedError == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, testCase.expectedError)
			}

			assert.Equal(t, testCase.canBeRerun, result)
		})
	}
}

func TestActionTask_GetAllAttempts(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	job2 := unittest.AssertExistsAndLoadBean(t, &ActionRunJob{ID: 192})

	allAttempts, err := job2.GetAllAttempts(t.Context())
	require.NoError(t, err)

	require.Len(t, allAttempts, 3)
	assert.EqualValues(t, 47, allAttempts[0].ID, "ordered by attempt, 1")
	assert.EqualValues(t, 53, allAttempts[1].ID, "ordered by attempt, 2")
	assert.EqualValues(t, 52, allAttempts[2].ID, "ordered by attempt, 3")

	// GetAllAttempts doesn't populate all fields; so check expected fields from one of the records
	assert.EqualValues(t, 3, allAttempts[0].Attempt, "read Attempt field")
	assert.Equal(t, StatusRunning, allAttempts[0].Status, "read Status field")
	assert.Equal(t, timeutil.TimeStamp(1683636528), allAttempts[0].Started, "read Started field")
}
