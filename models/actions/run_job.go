// Copyright 2022 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"context"
	"fmt"
	"slices"
	"time"

	"forgejo.org/models/db"
	"forgejo.org/modules/container"
	"forgejo.org/modules/timeutil"
	"forgejo.org/modules/util"

	"code.forgejo.org/forgejo/runner/v12/act/jobparser"
	gouuid "github.com/google/uuid"
	"go.yaml.in/yaml/v3"
	"xorm.io/builder"
)

// ActionRunJob represents a job of a run
type ActionRunJob struct {
	ID                int64
	RunID             int64      `xorm:"index"`
	Run               *ActionRun `xorm:"-"`
	RepoID            int64      `xorm:"index"`
	OwnerID           int64      `xorm:"index"`
	CommitSHA         string     `xorm:"index"`
	IsForkPullRequest bool
	Name              string `xorm:"VARCHAR(255)"`
	Attempt           int64
	Handle            string `xorm:"unique"`
	WorkflowPayload   []byte
	JobID             string   `xorm:"VARCHAR(255)"` // job id in workflow, not job's id
	Needs             []string `xorm:"JSON TEXT"`
	RunsOn            []string `xorm:"JSON TEXT"`
	TaskID            int64    // the latest task of the job
	Status            Status   `xorm:"index"`
	Started           timeutil.TimeStamp
	Stopped           timeutil.TimeStamp
	Created           timeutil.TimeStamp `xorm:"created"`
	Updated           timeutil.TimeStamp `xorm:"updated index"`

	workflowPayloadDecoded *jobparser.SingleWorkflow `xorm:"-"`
}

func init() {
	db.RegisterModel(new(ActionRunJob))
}

func (job *ActionRunJob) HTMLURL(ctx context.Context) (string, error) {
	if job.Run == nil || job.Run.Repo == nil {
		return "", fmt.Errorf("action_run_job: load run and repo before accessing HTMLURL")
	}

	// Find the "index" of the currently selected job... kinda ugly that the URL uses the index rather than some other
	// unique identifier of the job which could actually be stored upon it.  But hard to change that now.
	allJobs, err := GetRunJobsByRunID(ctx, job.RunID)
	if err != nil {
		return "", err
	}
	jobIndex := -1
	for i, otherJob := range allJobs {
		if job.ID == otherJob.ID {
			jobIndex = i
			break
		}
	}
	if jobIndex == -1 {
		return "", fmt.Errorf("action_run_job: unable to find job on run: %d", job.ID)
	}

	attempt := job.Attempt
	// If a job has never been fetched by a runner yet, it will have attempt 0 -- but this attempt will never have a
	// valid UI since attempt is incremented to 1 if it is picked up by a runner.
	if attempt == 0 {
		attempt = 1
	}

	return fmt.Sprintf("%s/actions/runs/%d/jobs/%d/attempt/%d", job.Run.Repo.HTMLURL(), job.Run.Index, jobIndex, attempt), nil
}

func (job *ActionRunJob) Duration() time.Duration {
	return calculateDuration(job.Started, job.Stopped, job.Status)
}

func (job *ActionRunJob) LoadRun(ctx context.Context) error {
	if job.Run == nil {
		run, err := GetRunByID(ctx, job.RunID)
		if err != nil {
			return err
		}
		job.Run = run
	}
	return nil
}

// LoadAttributes load Run if not loaded
func (job *ActionRunJob) LoadAttributes(ctx context.Context) error {
	if job == nil {
		return nil
	}

	if err := job.LoadRun(ctx); err != nil {
		return err
	}

	return job.Run.LoadAttributes(ctx)
}

// IsRequestedByRunner returns true if this attempt of this ActionRunJob was explicitly requested by the runner or if
// the runner expressed no preference.
func (job *ActionRunJob) IsRequestedByRunner(handle *string) bool {
	return handle == nil || job.Handle == *handle
}

func (job *ActionRunJob) ItRunsOn(labels []string) bool {
	if len(labels) == 0 || len(job.RunsOn) == 0 {
		return false
	}
	labelSet := make(container.Set[string])
	labelSet.AddMultiple(labels...)
	return labelSet.IsSubset(job.RunsOn)
}

func (job *ActionRunJob) PrepareNextAttempt(initialStatus Status) error {
	if job.Status != StatusUnknown && !job.Status.IsDone() {
		return fmt.Errorf("cannot prepare next attempt because job %d is active: %s", job.ID, job.Status.String())
	}

	job.Attempt++
	job.Started = 0
	job.Stopped = 0
	job.TaskID = 0
	job.Handle = gouuid.New().String()
	job.Status = initialStatus

	return nil
}

// CanBeRerun answers whether this ActionRunJob can be rerun. Returns true if it is done and the Run it belongs to
// is valid. Returns false in all other cases.
func (job *ActionRunJob) CanBeRerun(ctx context.Context) (bool, error) {
	if err := job.LoadRun(ctx); err != nil {
		return false, fmt.Errorf("cannot load run %d of job %d: %w", job.RunID, job.ID, err)
	}

	if !job.Run.IsValid() {
		return false, nil
	}
	return job.Status.IsDone(), nil
}

// GetAllAttempts retrieve all the attempts of this job. Limited fields are queried to avoid loading the LogIndexes blob
// when not needed.
func (job *ActionRunJob) GetAllAttempts(ctx context.Context) ([]*ActionTask, error) {
	var attempts []*ActionTask
	err := db.GetEngine(ctx).
		Cols("id", "attempt", "status", "started").
		Where("job_id=?", job.ID).
		Desc("attempt").
		Find(&attempts)
	if err != nil {
		return nil, err
	}
	return attempts, nil
}

func GetRunJobByID(ctx context.Context, id int64) (*ActionRunJob, error) {
	var job ActionRunJob
	has, err := db.GetEngine(ctx).Where("id=?", id).Get(&job)
	if err != nil {
		return nil, err
	} else if !has {
		return nil, fmt.Errorf("run job with id %d: %w", id, util.ErrNotExist)
	}

	return &job, nil
}

func GetRunJobsByRunID(ctx context.Context, runID int64) ([]*ActionRunJob, error) {
	var jobs []*ActionRunJob
	if err := db.GetEngine(ctx).Where("run_id=?", runID).OrderBy("id").Find(&jobs); err != nil {
		return nil, err
	}
	return jobs, nil
}

// Check if the ActionRun has any jobs other than those included in the jobs parameter.
func RunHasOtherJobs(ctx context.Context, runID int64, jobs []*ActionRunJob) (bool, error) {
	jobIDs := make([]int64, len(jobs))
	for i, job := range jobs {
		jobIDs[i] = job.ID
	}
	otherJobs, err := db.GetEngine(ctx).
		Where("run_id = ?", runID).
		Where(builder.NotIn("id", jobIDs)).
		Count(&ActionRunJob{})
	if err != nil {
		return false, err
	}
	return otherJobs > 0, nil
}

// All calls to UpdateRunJobWithoutNotification that change run.Status for any run from a not done status to a done status must call the ActionRunNowDone notification channel.
// Use the wrapper function UpdateRunJob instead.
func UpdateRunJobWithoutNotification(ctx context.Context, job *ActionRunJob, cond builder.Cond, cols ...string) (int64, error) {
	e := db.GetEngine(ctx)

	sess := e.ID(job.ID)
	if len(cols) > 0 {
		sess.Cols(cols...)
	}

	if cond != nil {
		sess.Where(cond)
	}

	affected, err := sess.Update(job)
	if err != nil {
		return 0, err
	}

	if affected == 0 || (!slices.Contains(cols, "status") && job.Status == 0) {
		return affected, nil
	}

	if affected != 0 && slices.Contains(cols, "status") && job.Status.IsWaiting() {
		// if the status of job changes to waiting again, increase tasks version.
		if err := IncreaseTaskVersion(ctx, job.OwnerID, job.RepoID); err != nil {
			return 0, err
		}
	}

	if job.RunID == 0 {
		var err error
		if job, err = GetRunJobByID(ctx, job.ID); err != nil {
			return 0, err
		}
	}

	run, columns, err := ComputeRunStatus(ctx, job.RunID)
	if err != nil {
		return 0, fmt.Errorf("compute run status: %w", err)
	}
	if err := UpdateRunWithoutNotification(ctx, run, columns...); err != nil {
		return 0, fmt.Errorf("update run %d: %w", run.ID, err)
	}

	return affected, nil
}

var AggregateJobStatus = func(jobs []*ActionRunJob) Status {
	allSuccessOrSkipped := len(jobs) != 0
	allSkipped := len(jobs) != 0
	var hasFailure, hasCancelled, hasWaiting, hasRunning, hasBlocked bool
	for _, job := range jobs {
		allSuccessOrSkipped = allSuccessOrSkipped && (job.Status == StatusSuccess || job.Status == StatusSkipped)
		allSkipped = allSkipped && job.Status == StatusSkipped
		hasFailure = hasFailure || job.Status == StatusFailure
		hasCancelled = hasCancelled || job.Status == StatusCancelled
		hasWaiting = hasWaiting || job.Status == StatusWaiting
		hasRunning = hasRunning || job.Status == StatusRunning
		hasBlocked = hasBlocked || job.Status == StatusBlocked
	}
	switch {
	case allSkipped:
		return StatusSkipped
	case allSuccessOrSkipped:
		return StatusSuccess
	case hasCancelled:
		return StatusCancelled
	case hasFailure:
		return StatusFailure
	case hasRunning:
		return StatusRunning
	case hasWaiting:
		return StatusWaiting
	case hasBlocked:
		return StatusBlocked
	default:
		return StatusUnknown // it shouldn't happen
	}
}

// Retrieves the parsed workflow for this specific job.  This field is often accessed multiple times in succession, so
// the parsed content is cached in-memory on the `ActionRunJob` instance.
func (job *ActionRunJob) DecodeWorkflowPayload() (*jobparser.SingleWorkflow, error) {
	if job.workflowPayloadDecoded != nil {
		return job.workflowPayloadDecoded, nil
	}

	var jobWorkflow jobparser.SingleWorkflow
	err := yaml.Unmarshal(job.WorkflowPayload, &jobWorkflow)
	if err != nil {
		return nil, fmt.Errorf("failure unmarshaling WorkflowPayload to SingleWorkflow: %w", err)
	}

	job.workflowPayloadDecoded = &jobWorkflow
	return job.workflowPayloadDecoded, nil
}

// If `WorkflowPayload` is changed on an `ActionRunJob`, clear any cached decoded version of the payload.  Typically
// only used for unit tests.
func (job *ActionRunJob) ClearCachedWorkflowPayload() {
	job.workflowPayloadDecoded = nil
}

// Checks whether the target job is an `(incomplete matrix)` job that will be blocked until the matrix is complete, and
// then regenerated and deleted.  If it is incomplete, and if the information is available, the specific job and/or
// output that causes it to be incomplete will be returned as well.
func (job *ActionRunJob) HasIncompleteMatrix() (bool, *jobparser.IncompleteNeeds, error) {
	jobWorkflow, err := job.DecodeWorkflowPayload()
	if err != nil {
		return false, nil, fmt.Errorf("failure decoding workflow payload: %w", err)
	}
	return jobWorkflow.IncompleteMatrix, jobWorkflow.IncompleteMatrixNeeds, nil
}

// Checks whether the target job has a `runs-on` field with an expression that requires an input from another job.  The
// job will be blocked until the other job is complete, and then regenerated and deleted.
func (job *ActionRunJob) HasIncompleteRunsOn() (bool, *jobparser.IncompleteNeeds, *jobparser.IncompleteMatrix, error) {
	jobWorkflow, err := job.DecodeWorkflowPayload()
	if err != nil {
		return false, nil, nil, fmt.Errorf("failure decoding workflow payload: %w", err)
	}
	return jobWorkflow.IncompleteRunsOn, jobWorkflow.IncompleteRunsOnNeeds, jobWorkflow.IncompleteRunsOnMatrix, nil
}

// Check whether the target job was generated as a result of expanding a reusable workflow.
func (job *ActionRunJob) IsWorkflowCallInnerJob() (bool, error) {
	jobWorkflow, err := job.DecodeWorkflowPayload()
	if err != nil {
		return false, fmt.Errorf("failure decoding workflow payload: %w", err)
	}
	return jobWorkflow.Metadata.WorkflowCallParent != "", nil
}

// Check whether this job is a caller of a reusable workflow -- in other words, the real work done in this job is in
// spawned child jobs, not this job.
func (job *ActionRunJob) IsWorkflowCallOuterJob() (bool, error) {
	jobWorkflow, err := job.DecodeWorkflowPayload()
	if err != nil {
		return false, fmt.Errorf("failure decoding workflow payload: %w", err)
	}
	return jobWorkflow.Metadata.WorkflowCallID != "", nil
}

// Checks whether the target job has a `with` field with an expression that requires an input from another job.  The job
// will be blocked until the other job is complete, and then regenerated and deleted.
func (job *ActionRunJob) HasIncompleteWith() (bool, *jobparser.IncompleteNeeds, *jobparser.IncompleteMatrix, error) {
	jobWorkflow, err := job.DecodeWorkflowPayload()
	if err != nil {
		return false, nil, nil, fmt.Errorf("failure decoding workflow payload: %w", err)
	}
	return jobWorkflow.IncompleteWith, jobWorkflow.IncompleteWithNeeds, jobWorkflow.IncompleteWithMatrix, nil
}

// EnableOpenIDConnect checks whether the job allows for ID token generation.
func (job *ActionRunJob) EnableOpenIDConnect() (bool, error) {
	jobWorkflow, err := job.DecodeWorkflowPayload()
	if err != nil {
		return false, fmt.Errorf("failure decoding workflow payload: %w", err)
	}
	return jobWorkflow.EnableOpenIDConnect, nil
}

// AllNeedsExist checks whether this ActionRunJob's Needs can theoretically be met by comparing them with the supplied
// list of all job IDs that part of a particular workflow run. Returns the list of unknown job IDs found in Needs
// alongside an indicator whether the check was successful.
func (job *ActionRunJob) AllNeedsExist(allExistingJobIDs container.Set[string]) ([]string, bool) {
	unknownJobIDs := []string{}
	for _, need := range job.Needs {
		if !allExistingJobIDs.Contains(need) {
			unknownJobIDs = append(unknownJobIDs, need)
		}
	}

	return unknownJobIDs, len(unknownJobIDs) == 0
}
