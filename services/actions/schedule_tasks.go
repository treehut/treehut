// Copyright 2023 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	actions_model "forgejo.org/models/actions"
	"forgejo.org/models/db"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unit"
	actions_module "forgejo.org/modules/actions"
	"forgejo.org/modules/log"
	"forgejo.org/modules/timeutil"
	webhook_module "forgejo.org/modules/webhook"

	"code.forgejo.org/forgejo/runner/v12/act/jobparser"
	act_model "code.forgejo.org/forgejo/runner/v12/act/model"
	"github.com/gdgvda/cron"
)

// StartScheduleTasks start the task
func StartScheduleTasks(ctx context.Context) error {
	return startTasks(ctx)
}

// startTasks retrieves specifications in pages, creates a schedule task for each specification,
// and updates the specification's next run time and previous run time.
// The function returns an error if there's an issue with finding or updating the specifications.
func startTasks(ctx context.Context) error {
	// Set the page size
	pageSize := 50

	// Retrieve specs in pages until all specs have been retrieved
	now := time.Now()
	for page := 1; ; page++ {
		// Retrieve the specs for the current page
		specs, _, err := actions_model.FindSpecs(ctx, actions_model.FindSpecOptions{
			ListOptions: db.ListOptions{
				Page:     page,
				PageSize: pageSize,
			},
			Next: now.Unix(),
		})
		if err != nil {
			return fmt.Errorf("find specs: %w", err)
		}

		if err := specs.LoadRepos(ctx); err != nil {
			return fmt.Errorf("LoadRepos: %w", err)
		}

		// Loop through each spec and create a schedule task for it
		for _, row := range specs {
			if row.Repo.IsArchived {
				// Skip if the repo is archived
				continue
			}

			cfg, err := row.Repo.GetUnit(ctx, unit.TypeActions)
			if err != nil {
				if repo_model.IsErrUnitTypeNotExist(err) {
					// Skip the actions unit of this repo is disabled.
					continue
				}
				return fmt.Errorf("GetUnit: %w", err)
			}
			actionConfig := cfg.ActionsConfig()
			if actionConfig.IsWorkflowDisabled(row.Schedule.WorkflowID) {
				continue
			}

			createAndSchedule := func(row *actions_model.ActionScheduleSpec) (cron.Schedule, error) {
				if err := CreateScheduleTask(ctx, row.Schedule); err != nil {
					return nil, fmt.Errorf("CreateScheduleTask: %v", err)
				}

				// Parse the spec
				schedule, err := row.Parse()
				if err != nil {
					return nil, fmt.Errorf("Parse(Spec=%v): %v", row.Spec, err)
				}
				return schedule, nil
			}

			schedule, err := createAndSchedule(row)
			if err != nil {
				log.Error("RepoID=%v WorkflowID=%v: %v", row.Schedule.RepoID, row.Schedule.WorkflowID, err)
				actionConfig.DisableWorkflow(row.Schedule.WorkflowID)
				if err := repo_model.UpdateRepoUnit(ctx, cfg); err != nil {
					log.Error("RepoID=%v WorkflowID=%v: CreateScheduleTask: %v", row.Schedule.RepoID, row.Schedule.WorkflowID, err)
					return err
				}
				continue
			}

			// Update the spec's next run time and previous run time
			row.Prev = row.Next
			row.Next = timeutil.TimeStamp(schedule.Next(now.Add(1 * time.Minute)).Unix())
			if err := actions_model.UpdateScheduleSpec(ctx, row, "prev", "next"); err != nil {
				log.Error("UpdateScheduleSpec: %v", err)
				return err
			}
		}

		// Stop if all specs have been retrieved
		if len(specs) < pageSize {
			break
		}
	}

	return nil
}

// CreateScheduleTask creates a scheduled task from a cron action schedule.
// It creates an action run based on the schedule, inserts it into the database, and creates commit statuses for each job.
func CreateScheduleTask(ctx context.Context, cron *actions_model.ActionSchedule) error {
	// Create a new action run based on the schedule
	run := &actions_model.ActionRun{
		Title:             cron.Title,
		RepoID:            cron.RepoID,
		OwnerID:           cron.OwnerID,
		WorkflowID:        cron.WorkflowID,
		WorkflowDirectory: cron.WorkflowDirectory,
		TriggerUserID:     cron.TriggerUserID,
		Ref:               cron.Ref,
		CommitSHA:         cron.CommitSHA,
		Event:             cron.Event,
		EventPayload:      cron.EventPayload,
		TriggerEvent:      string(webhook_module.HookEventSchedule),
		ScheduleID:        cron.ID,
		Status:            actions_model.StatusWaiting,
	}

	vars, err := actions_model.GetVariablesOfRun(ctx, run)
	if err != nil {
		log.Error("GetVariablesOfRun: %v", err)
		return err
	}

	workflow, err := act_model.ReadWorkflow(bytes.NewReader(cron.Content), false)
	if err != nil {
		return err
	}
	notifications, err := workflow.Notifications()
	if err != nil {
		return err
	}
	run.NotifyEmail = notifications

	err = ConfigureActionRunConcurrency(workflow, run, vars, map[string]any{})
	if err != nil {
		return err
	}

	if run.ConcurrencyType == actions_model.CancelInProgress {
		if err := CancelPreviousWithConcurrencyGroup(
			ctx,
			run.RepoID,
			run.ConcurrencyGroup,
		); err != nil {
			return err
		}
	}

	// In the event that local reusable workflows (eg. `uses: ./.forgejo/workflows/reusable.yml`) are present, we'll
	// need to read the commit of the schedule to resolve that reference:
	expandLocalReusableWorkflow, expandCleanup := lazyRepoExpandLocalReusableWorkflow(ctx, cron.RepoID, cron.CommitSHA)
	defer expandCleanup()

	// Parse the workflow specification from the cron schedule
	workflows, err := actions_module.JobParser(cron.Content,
		jobparser.WithVars(vars),
		// We don't have any job outputs yet, but `WithJobOutputs(...)` triggers JobParser to supporting its
		// `IncompleteMatrix` tagging for any jobs that require the inputs of other jobs.
		jobparser.WithJobOutputs(map[string]map[string]string{}),
		jobparser.SupportIncompleteRunsOn(),
		jobparser.ExpandLocalReusableWorkflows(expandLocalReusableWorkflow),
		jobparser.ExpandInstanceReusableWorkflows(expandInstanceReusableWorkflows(ctx)),
	)
	if err != nil {
		return err
	}

	// Insert the action run and its associated jobs into the database
	if err := actions_model.InsertRun(ctx, run, workflows); err != nil {
		return err
	}

	if err := consistencyCheckRun(ctx, run); err != nil {
		return err
	}

	// Return nil if no errors occurred
	return nil
}

// CancelPreviousJobs cancels all previous jobs of the same repository, reference, workflow, and event.
// It's useful when a new run is triggered, and all previous runs needn't be continued anymore.
func CancelPreviousJobs(ctx context.Context, repoID int64, ref, workflowID string, event webhook_module.HookEventType) error {
	// Find all runs in the specified repository, reference, and workflow with non-final status
	runs, _, err := db.FindAndCount[actions_model.ActionRun](ctx, actions_model.FindRunOptions{
		RepoID:       repoID,
		Ref:          ref,
		WorkflowID:   workflowID,
		TriggerEvent: event,
		Status:       []actions_model.Status{actions_model.StatusRunning, actions_model.StatusWaiting, actions_model.StatusBlocked},
	})
	if err != nil {
		return err
	}

	// Iterate over each found run and cancel its associated jobs.
	errorSlice := []error{}
	for _, run := range runs {
		err := killRun(ctx, run, actions_model.StatusCancelled)
		errorSlice = append(errorSlice, err)
	}
	err = errors.Join(errorSlice...)
	if err != nil {
		return err
	}

	return nil
}

// Cancels all pending jobs in the same repository with the same concurrency group.
func CancelPreviousWithConcurrencyGroup(ctx context.Context, repoID int64, concurrencyGroup string) error {
	// Find all runs in the concurrency group which have at least one job that is still pending; we can't use the run's
	// status for this because runs are set to failed if a single job is marked as failed, even if other jobs are still
	// running.
	runs := make([]*actions_model.ActionRun, 0, 10)
	if err := db.GetEngine(ctx).Table("action_run").
		Join("INNER", "action_run_job", "action_run_job.run_id = action_run.id").
		Where("action_run.repo_id = ? AND action_run.concurrency_group = ?", repoID, strings.ToLower(concurrencyGroup)).
		In("action_run_job.status", actions_model.PendingStatuses()).
		Distinct("action_run.id").
		Select("action_run.id,action_run.need_approval").
		Find(&runs); err != nil {
		return err
	}

	// Iterate over each found run and cancel its associated jobs.
	errorSlice := []error{}
	for _, run := range runs {
		err := killRun(ctx, run, actions_model.StatusCancelled)
		errorSlice = append(errorSlice, err)
	}
	err := errors.Join(errorSlice...)
	if err != nil {
		return err
	}

	return nil
}

func CleanRepoScheduleTasks(ctx context.Context, repo *repo_model.Repository, cancelPreviousJobs bool) error {
	// If actions disabled when there is schedule task, this will remove the outdated schedule tasks
	// There is no other place we can do this because the app.ini will be changed manually
	if err := actions_model.DeleteScheduleTaskByRepo(ctx, repo.ID); err != nil {
		return fmt.Errorf("DeleteCronTaskByRepo: %v", err)
	}
	if cancelPreviousJobs {
		// cancel running cron jobs of this repository and delete old schedules
		if err := CancelPreviousJobs(
			ctx,
			repo.ID,
			repo.DefaultBranch,
			"",
			webhook_module.HookEventSchedule,
		); err != nil {
			return fmt.Errorf("CancelPreviousJobs: %v", err)
		}
	}
	return nil
}
