// Copyright 2024 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"context"
	"errors"
	"fmt"
	"slices"

	actions_model "forgejo.org/models/actions"
	"forgejo.org/models/db"
	"forgejo.org/models/unit"
	"forgejo.org/modules/container"
	"forgejo.org/modules/timeutil"
	"forgejo.org/modules/util"

	"xorm.io/builder"
)

var (
	// ErrRerunWorkflowInvalid signals that the workflow cannot be run because it is invalid, for example, due to syntax
	// errors.
	ErrRerunWorkflowInvalid = errors.New("workflow is invalid")
	// ErrRerunWorkflowDisabled indicates that the workflow cannot be run because it has been disabled by the user or
	// Forgejo.
	ErrRerunWorkflowDisabled = errors.New("workflow is disabled")
	// ErrRerunWorkflowStillRunning signals that the workflow cannot be rerun because at least one job is still running.
	ErrRerunWorkflowStillRunning = errors.New("workflow is still running")
	// ErrRerunJobStillRunning signals that the job cannot be rerun because it is still running.
	ErrRerunJobStillRunning = errors.New("job is still running")
)

// GetAllRerunJobs get all jobs that need to be rerun when job should be rerun
func GetAllRerunJobs(job *actions_model.ActionRunJob, allJobs []*actions_model.ActionRunJob) []*actions_model.ActionRunJob {
	rerunJobs := []*actions_model.ActionRunJob{job}
	rerunJobsIDSet := make(container.Set[string])
	rerunJobsIDSet.Add(job.JobID)

	for _, j := range allJobs {
		if rerunJobsIDSet.Contains(j.JobID) {
			continue
		}
		if slices.ContainsFunc(j.Needs, rerunJobsIDSet.Contains) {
			rerunJobs = append(rerunJobs, j)
			rerunJobsIDSet.Add(j.JobID)
		}
	}

	return rerunJobs
}

// RerunAllJobs reruns all jobs of the given run and returns them. For it to succeed, the workflow must be valid, and the
// previous run must have completed.
func RerunAllJobs(ctx context.Context, run *actions_model.ActionRun) ([]*actions_model.ActionRunJob, error) {
	if !run.IsValid() {
		return nil, ErrRerunWorkflowInvalid
	}
	if !run.Status.IsDone() {
		return nil, ErrRerunWorkflowStillRunning
	}

	if err := run.LoadRepo(ctx); err != nil {
		return nil, fmt.Errorf("cannot load repo of run %d: %w", run.ID, err)
	}

	actionsConfig := run.Repo.MustGetUnit(ctx, unit.TypeActions).ActionsConfig()
	if actionsConfig.IsWorkflowDisabled(run.WorkflowID) {
		return nil, ErrRerunWorkflowDisabled
	}

	var rerunJobs []*actions_model.ActionRunJob
	if err := db.WithTx(ctx, func(ctx context.Context) error {
		if run.Status != actions_model.StatusUnknown && !run.Status.IsDone() {
			return fmt.Errorf("cannot prepare next attempt because run %d is active: %s", run.ID, run.Status.String())
		}

		// Wipe all artifacts before a rerun to prevent stale artifacts from polluting artifacts collected during the
		// rerun.
		if err := actions_model.SetArtifactsOfRunDeleted(ctx, run.ID); err != nil {
			return fmt.Errorf("cannot remove artifacts of previous run of run %d: %w", run.ID, err)
		}

		run.PreviousDuration = run.Duration()

		run.Status = actions_model.StatusWaiting
		run.Started = 0
		run.Stopped = 0

		// The columns have to be specified here to work around a xorm quirk: It won't update columns that are set to
		// their zero value without AllCols().
		if err := UpdateRun(ctx, run, "status", "started", "stopped", "previous_duration"); err != nil {
			return fmt.Errorf("cannot update run %d: %w", run.ID, err)
		}

		jobs, err := actions_model.GetRunJobsByRunID(ctx, run.ID)
		if err != nil {
			return fmt.Errorf("could not load jobs of run %d: %w", run.ID, err)
		}

		for _, job := range jobs {
			initialStatus := actions_model.StatusWaiting
			if len(job.Needs) > 0 {
				initialStatus = actions_model.StatusBlocked
			}

			if err := rerunSingleJob(ctx, job, initialStatus); err != nil {
				return fmt.Errorf("could not rerun job %d of run %d: %w", job.ID, run.ID, err)
			}

			rerunJobs = append(rerunJobs, job)
		}

		return nil
	}); err != nil {
		return nil, err
	}
	return rerunJobs, nil
}

// RerunJob reruns the given job and all its dependent jobs. It returns all jobs that were rerun. For it to succeed, the
// workflow that defines this job must be valid, and the previous run must have completed. Dependent jobs that have not
// completed yet are ignored.
func RerunJob(ctx context.Context, job *actions_model.ActionRunJob) ([]*actions_model.ActionRunJob, error) {
	if err := job.LoadAttributes(ctx); err != nil {
		return nil, fmt.Errorf("cannot load attributes of job %d: %w", job.ID, err)
	}
	if !job.Run.IsValid() {
		return nil, ErrRerunWorkflowInvalid
	}

	actionsConfig := job.Run.Repo.MustGetUnit(ctx, unit.TypeActions).ActionsConfig()
	if actionsConfig.IsWorkflowDisabled(job.Run.WorkflowID) {
		return nil, ErrRerunWorkflowDisabled
	}

	if !job.Status.IsDone() {
		return nil, ErrRerunJobStillRunning
	}

	var rerunJobs []*actions_model.ActionRunJob
	if err := db.WithTx(ctx, func(ctx context.Context) error {
		if job.Run.Status.IsUnknown() || job.Run.Status.IsDone() {
			job.Run.PreviousDuration = job.Run.Duration()
			job.Run.Status = actions_model.StatusWaiting
			job.Run.Started = 0
			job.Run.Stopped = 0

			if err := UpdateRun(ctx, job.Run, "previous_duration", "status", "started", "stopped"); err != nil {
				return fmt.Errorf("unable to update run %d of job %d: %w", job.RunID, job.ID, err)
			}
		}

		jobs, err := actions_model.GetRunJobsByRunID(ctx, job.RunID)
		if err != nil {
			return fmt.Errorf("could not load jobs of run %d: %w", job.RunID, err)
		}

		// Wipe all artifacts before a rerun to prevent stale artifacts from polluting the artifacts collected during
		// the rerun. Because artifacts are bound to a run and not to a job, it is not possible to only remove the
		// artifacts of the jobs that are going to be rerun. That means that artifacts created by jobs that are not
		// rerun will be lost. That matches GitHub Actions' behaviour as of May 2026.
		if err := actions_model.SetArtifactsOfRunDeleted(ctx, job.RunID); err != nil {
			return fmt.Errorf("cannot remove artifacts of previous run of run %d: %w", job.RunID, err)
		}

		for _, jobToRerun := range GetAllRerunJobs(job, jobs) {
			// If the dependent job is still running, cancel it so that it can be rerun, too. Its results are obsolete
			// when the job it depends on is rerun.
			if !jobToRerun.Status.IsDone() {
				if err := cancelSingleJob(ctx, jobToRerun, actions_model.StatusCancelled); err != nil {
					return fmt.Errorf("cannot cancel dependent job %d with status %s: %w",
						jobToRerun.ID, jobToRerun.Status, err)
				}

				// Refresh the job after cancellation.
				if jobToRerun, err = actions_model.GetRunJobByID(ctx, jobToRerun.ID); err != nil {
					return fmt.Errorf("cannot refresh cancelled dependent job %d: %w", jobToRerun.ID, err)
				}
			}

			canBeRerun, err := jobToRerun.CanBeRerun(ctx)
			if err != nil {
				return fmt.Errorf("cannot determine whether job %d can be rerun: %w", jobToRerun.ID, err)
			}

			// This should never happen because the run was validated and the job cancelled if it was running.
			if !canBeRerun {
				return fmt.Errorf("cannot rerun dependent job %d", jobToRerun.ID)
			}

			// The job that should be rerun cannot be blocked, even if it has needs.
			initialStatus := actions_model.StatusWaiting
			if len(jobToRerun.Needs) > 0 && jobToRerun.ID != job.ID {
				initialStatus = actions_model.StatusBlocked
			}

			if err := rerunSingleJob(ctx, jobToRerun, initialStatus); err != nil {
				return fmt.Errorf("cannot rerun job %d: %w", jobToRerun.ID, err)
			}
			rerunJobs = append(rerunJobs, jobToRerun)
		}
		return nil
	}); err != nil {
		return nil, err
	}

	return rerunJobs, nil
}

func rerunSingleJob(ctx context.Context, job *actions_model.ActionRunJob, initialStatus actions_model.Status) error {
	oldStatus := job.Status

	if err := job.PrepareNextAttempt(initialStatus); err != nil {
		return err
	}

	// The columns have to be specified here to work around a xorm quirk: It won't update columns that are set to their
	// zero value without AllCols().
	if _, err := UpdateRunJob(ctx, job, builder.Eq{"status": oldStatus}, "handle", "attempt", "task_id", "status", "started", "stopped"); err != nil {
		return err
	}

	CreateCommitStatus(ctx, job)

	return nil
}

// cancelSingleJob cancels the given job and its associated task, if any. outcomeStatus defines the status that should
// be assigned to the cancelled job and its associated task after cancellation; a non-terminal status will result in an
// error. Nothing happens if the job has already been completed.
func cancelSingleJob(ctx context.Context, job *actions_model.ActionRunJob, outcomeStatus actions_model.Status) error {
	if !outcomeStatus.IsDone() {
		return fmt.Errorf("outcomeStatus must be a terminal status, but is: %s", outcomeStatus)
	}
	if job.Status.IsDone() {
		return nil
	}

	return db.WithTx(ctx, func(ctx context.Context) error {
		if job.TaskID == 0 {
			job.Status = outcomeStatus
			job.Stopped = timeutil.TimeStampNow()
			_, err := UpdateRunJob(ctx, job, nil, "status", "stopped")
			if err != nil {
				return fmt.Errorf("could not cancel job %d: %w", job.ID, err)
			}
		}

		// A task might have been created while we're trying to cancel the job. Therefore, always try to stop the task.
		if err := StopTask(ctx, job.TaskID, outcomeStatus); err != nil {
			if errors.Is(err, util.ErrNotExist) {
				return nil
			}
			return err
		}
		return nil
	})
}
