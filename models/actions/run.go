// Copyright 2022 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"forgejo.org/models/db"
	repo_model "forgejo.org/models/repo"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/cache"
	"forgejo.org/modules/git"
	"forgejo.org/modules/json"
	"forgejo.org/modules/log"
	"forgejo.org/modules/optional"
	api "forgejo.org/modules/structs"
	"forgejo.org/modules/timeutil"
	"forgejo.org/modules/util"
	webhook_module "forgejo.org/modules/webhook"

	"code.forgejo.org/forgejo/runner/v12/act/jobparser"
	"xorm.io/builder"
)

type ConcurrencyMode int

const (
	// Don't enforce concurrency control.  Note that you won't find `UnlimitedConcurrency` implemented directly in the
	// code; setting it on an `ActionRun` prevents the other limiting behaviors.
	UnlimitedConcurrency ConcurrencyMode = iota
	// Queue behind other jobs with the same concurrency group
	QueueBehind
	// Cancel other jobs with the same concurrency group
	CancelInProgress
)

// ActionRun represents a run of a workflow file
type ActionRun struct {
	ID                   int64
	Title                string
	RepoID               int64                  `xorm:"index unique(repo_index) index(concurrency)"`
	Repo                 *repo_model.Repository `xorm:"-"`
	OwnerID              int64                  `xorm:"index"`
	WorkflowID           string                 `xorm:"index"`                                 // the name of workflow file
	WorkflowDirectory    string                 `xorm:"NOT NULL DEFAULT '.forgejo/workflows'"` // directory where the workflow file resides, for example, .forgejo/workflows
	Index                int64                  `xorm:"index unique(repo_index)"`              // a unique number for each run of a repository
	TriggerUserID        int64                  `xorm:"index"`
	TriggerUser          *user_model.User       `xorm:"-"`
	ScheduleID           int64
	Ref                  string `xorm:"index"` // the commit/tag/… that caused the run
	IsRefDeleted         bool   `xorm:"-"`
	CommitSHA            string
	WorkflowSourceCommit optional.Option[string]      // typically NULL indicating equality w/ CommitSHA, except for `pull_request_target` where it indicates the base branch's commit at time of execution
	Event                webhook_module.HookEventType // the webhook event that causes the workflow to run
	EventPayload         string                       `xorm:"LONGTEXT"`
	TriggerEvent         string                       // the trigger event defined in the `on` configuration of the triggered workflow
	Status               Status                       `xorm:"index"`
	Version              int                          `xorm:"version default 0"` // Status could be updated concomitantly, so an optimistic lock is needed
	// Started and Stopped is used for recording last run time, if rerun happened, they will be reset to 0
	Started timeutil.TimeStamp
	Stopped timeutil.TimeStamp
	// PreviousDuration is used for recording previous duration
	PreviousDuration time.Duration
	Created          timeutil.TimeStamp `xorm:"created"`
	Updated          timeutil.TimeStamp `xorm:"updated"`
	NotifyEmail      bool

	// pull request trust
	IsForkPullRequest   bool
	PullRequestPosterID int64
	PullRequestID       int64 `xorm:"index"`
	NeedApproval        bool
	ApprovedBy          int64 `xorm:"index"`

	ConcurrencyGroup string `xorm:"'concurrency_group' index(concurrency)"`
	ConcurrencyType  ConcurrencyMode

	// used to report errors that blocked execution of a workflow
	PreExecutionError          string `xorm:"LONGTEXT"` // deprecated: replaced with PreExecutionErrorCode and PreExecutionErrorDetails for better i18n
	PreExecutionErrorCode      PreExecutionError
	PreExecutionErrorDetails   []any `xorm:"JSON LONGTEXT"`
	PreExecutionWarningCodes   []PreExecutionWarning
	PreExecutionWarningDetails [][]any `xorm:"JSON LONGTEXT"`

	// Priority defines the numerical order in which tasks should be processed (best effort). Tasks with the highest
	// numbers are processed first. The value range is between -128 and +127; 0 is the default value.
	Priority int8 `xorm:"NOT NULL DEFAULT 0"`
	// Prioritize signals whether a user has requested that this run should be prioritized (`true`). It is a separate
	// value so that it does not get lost when prioritization algorithms change the ActionRun's Priority.
	Prioritize bool `xorm:"NOT NULL DEFAULT false"`
}

func init() {
	db.RegisterModel(new(ActionRun))
	db.RegisterModel(new(ActionRunIndex))
}

func (run *ActionRun) HTMLURL() string {
	if run.Repo == nil {
		return ""
	}
	return fmt.Sprintf("%s/actions/runs/%d", run.Repo.HTMLURL(), run.Index)
}

func (run *ActionRun) Link() string {
	if run.Repo == nil {
		return ""
	}
	return fmt.Sprintf("%s/actions/runs/%d", run.Repo.Link(), run.Index)
}

func (run *ActionRun) CommitLink() string {
	if run.Repo == nil {
		return ""
	}
	return fmt.Sprintf("%s/commit/%s", run.Repo.Link(), run.CommitSHA)
}

// WorkflowPath returns the path in the git repo to the workflow file that this run was based on
func (run *ActionRun) WorkflowPath() string {
	if run.WorkflowDirectory == "" {
		return run.WorkflowID
	}
	return run.WorkflowDirectory + "/" + run.WorkflowID
}

// RefLink return the url of run's ref
func (run *ActionRun) RefLink() string {
	refName := git.RefName(run.Ref)
	if refName.IsPull() {
		return run.Repo.Link() + "/pulls/" + refName.ShortName()
	}
	return git.RefURL(run.Repo.Link(), run.Ref)
}

// PrettyRef return #id for pull ref or ShortName for others
func (run *ActionRun) PrettyRef() string {
	refName := git.RefName(run.Ref)
	if refName.IsPull() {
		return "#" + strings.TrimSuffix(strings.TrimPrefix(run.Ref, git.PullPrefix), "/head")
	}
	return refName.ShortName()
}

// LoadAttributes load Repo TriggerUser if not loaded
func (run *ActionRun) LoadAttributes(ctx context.Context) error {
	if run == nil {
		return nil
	}

	if err := run.LoadRepo(ctx); err != nil {
		return err
	}

	if err := run.Repo.LoadAttributes(ctx); err != nil {
		return err
	}

	if run.TriggerUser == nil {
		u, err := user_model.GetPossibleUserByID(ctx, run.TriggerUserID)
		if user_model.IsErrUserNotExist(err) {
			u = user_model.NewGhostUser()
		} else if err != nil {
			return err
		}
		run.TriggerUser = u
	}

	return nil
}

func (run *ActionRun) LoadRepo(ctx context.Context) error {
	if run == nil || run.Repo != nil {
		return nil
	}

	repo, err := repo_model.GetRepositoryByID(ctx, run.RepoID)
	if err != nil {
		return err
	}
	run.Repo = repo
	return nil
}

func (run *ActionRun) Duration() time.Duration {
	return calculateDuration(run.Started, run.Stopped, run.Status) + run.PreviousDuration
}

func (run *ActionRun) GetPushEventPayload() (*api.PushPayload, error) {
	if run.Event == webhook_module.HookEventPush {
		var payload api.PushPayload
		if err := json.Unmarshal([]byte(run.EventPayload), &payload); err != nil {
			return nil, err
		}
		return &payload, nil
	}
	return nil, fmt.Errorf("event %s is not a push event", run.Event)
}

func (run *ActionRun) GetPullRequestEventPayload() (*api.PullRequestPayload, error) {
	if run.Event == webhook_module.HookEventPullRequest ||
		run.Event == webhook_module.HookEventPullRequestSync ||
		run.Event == webhook_module.HookEventPullRequestAssign ||
		run.Event == webhook_module.HookEventPullRequestMilestone ||
		run.Event == webhook_module.HookEventPullRequestLabel {
		var payload api.PullRequestPayload
		if err := json.Unmarshal([]byte(run.EventPayload), &payload); err != nil {
			return nil, err
		}
		return &payload, nil
	}
	return nil, fmt.Errorf("event %s is not a pull request event", run.Event)
}

func (run *ActionRun) SetConcurrencyGroup(concurrencyGroup string) {
	// Concurrency groups are case insensitive identifiers, implemented by collapsing case here.  Unfortunately the
	// `ConcurrencyGroup` field can't be made a private field because xorm doesn't map those fields -- using
	// `SetConcurrencyGroup` is required for consistency but not enforced at compile-time.
	run.ConcurrencyGroup = strings.ToLower(concurrencyGroup)
}

func (run *ActionRun) SetDefaultConcurrencyGroup() {
	// Before ConcurrencyGroups were supported, Forgejo would automatically cancel runs with matching git refs, workflow
	// IDs, and trigger events.  For backwards compatibility we emulate that behavior:
	run.SetConcurrencyGroup(fmt.Sprintf(
		"%s_%s_%s__auto",
		run.Ref,
		run.WorkflowID,
		run.TriggerEvent,
	))
}

func (run *ActionRun) FindOuterWorkflowCall(ctx context.Context, innerCall *ActionRunJob) (*ActionRunJob, error) {
	allJobs, err := GetRunJobsByRunID(ctx, run.ID)
	if err != nil {
		return nil, fmt.Errorf("failure to get run jobs: %w", err)
	}
	if innerCall.workflowPayloadDecoded == nil || innerCall.workflowPayloadDecoded.Metadata.WorkflowCallParent == "" {
		return nil, errors.New("invalid state for FindOuterWorkflowCall")
	}
	parent := innerCall.workflowPayloadDecoded.Metadata.WorkflowCallParent
	for _, job := range allJobs {
		if job.ID == innerCall.ID {
			continue
		}
		swf, err := job.DecodeWorkflowPayload()
		if err != nil {
			return nil, err
		}
		if swf.Metadata.WorkflowCallID == parent {
			return job, nil
		}
	}
	return nil, fmt.Errorf("no workflow call with ID %s found in run %d", parent, run.ID)
}

func (run *ActionRun) IsScheduledRun() bool {
	return run.TriggerEvent == "schedule"
}

func (run *ActionRun) IsDispatchedRun() bool {
	return run.TriggerEvent == "workflow_dispatch"
}

// IsValid indicates whether this ActionRun is valid and can be run.
func (run *ActionRun) IsValid() bool {
	return run.PreExecutionErrorCode == 0 && run.PreExecutionError == ""
}

// CanBeRerun indicates whether this ActionRun can be rerun.
func (run *ActionRun) CanBeRerun() bool {
	if !run.IsValid() {
		return false
	}
	return run.Status.IsDone()
}

func (run *ActionRun) PrepareNextAttempt() error {
	if run.Status != StatusUnknown && !run.Status.IsDone() {
		return fmt.Errorf("cannot prepare next attempt because run %d is active: %s", run.ID, run.Status.String())
	}

	run.PreviousDuration = run.Duration()

	run.Status = StatusWaiting
	run.Started = 0
	run.Stopped = 0
	run.Priority = DefaultRunPriority
	run.Prioritize = false

	return nil
}

// Return the commit, in `RepoID`, which should be used for sourcing workflows for this run.  Typically this is the same
// as CommitSHA, but in workflows which are executed by the `pull_request_target` trigger this will be a commit from the
// pull request target, in other words the base branch of the PR, not the head.
func (run *ActionRun) GetWorkflowSourceCommit() string {
	if hasStoredSourceCommit, storedSourceCommit := run.WorkflowSourceCommit.Get(); hasStoredSourceCommit {
		return storedSourceCommit
	}
	return run.CommitSHA
}

func actionsCountOpenCacheKey(repoID int64) string {
	return fmt.Sprintf("Actions:CountOpenActionRuns:%d", repoID)
}

func RepoNumOpenActions(ctx context.Context, repoID int64) int {
	num, err := cache.GetInt(actionsCountOpenCacheKey(repoID), func() (int, error) {
		count, err := db.GetEngine(ctx).
			Table("action_run").
			Where(
				builder.Eq{"repo_id": repoID}.And(
					builder.In("status", PendingStatuses()))).
			Count()
		if err != nil {
			return 0, fmt.Errorf("query error: %v", err)
		}
		return int(count), nil
	})
	if err != nil {
		log.Error("failed to retrieve NumIssues: %v", err)
		return 0
	}
	return num
}

func clearRepoRunCountCache(ctx context.Context, repo *repo_model.Repository) {
	db.AfterTx(ctx, func() {
		cache.Remove(actionsCountOpenCacheKey(repo.ID))
	})
}

func condRunsThatNeedApproval(repoID, pullRequestID int64) builder.Cond {
	// performance relies indexes on repo_id and pull_request_id
	return builder.Eq{"repo_id": repoID, "pull_request_id": pullRequestID, "need_approval": true}
}

func GetRunsThatNeedApprovalByRepoIDAndPullRequestID(ctx context.Context, repoID, pullRequestID int64) ([]*ActionRun, error) {
	var runs []*ActionRun
	if err := db.GetEngine(ctx).Where(condRunsThatNeedApproval(repoID, pullRequestID)).Find(&runs); err != nil {
		return nil, err
	}
	return runs, nil
}

func HasRunThatNeedApproval(ctx context.Context, repoID, pullRequestID int64) (bool, error) {
	return db.GetEngine(ctx).Where(condRunsThatNeedApproval(repoID, pullRequestID)).Exist(&ActionRun{})
}

type ApprovalType bool

const (
	NeedApproval        = ApprovalType(true)
	DoesNotNeedApproval = ApprovalType(false)
	UndefinedApproval   = ApprovalType(false)
)

func UpdateRunApprovalByID(ctx context.Context, id int64, approval ApprovalType, approvedBy int64) error {
	_, err := db.GetEngine(ctx).Exec("UPDATE action_run SET need_approval=?, approved_by=? WHERE id=?", bool(approval), approvedBy, id)
	return err
}

func GetRunsNotDoneByRepoIDAndPullRequestPosterID(ctx context.Context, repoID, pullRequestPosterID int64) ([]*ActionRun, error) {
	var runs []*ActionRun
	// performance relies on indexes on repo_id and status
	if err := db.GetEngine(ctx).Where("repo_id=? AND pull_request_poster_id=?", repoID, pullRequestPosterID).And(builder.In("status", PendingStatuses())).Find(&runs); err != nil {
		return nil, err
	}
	return runs, nil
}

func GetRunsNotDoneByRepoIDAndPullRequestID(ctx context.Context, repoID, pullRequestID int64) ([]*ActionRun, error) {
	var runs []*ActionRun
	// performance relies on indexes on repo_id and status
	if err := db.GetEngine(ctx).Where("repo_id=? AND pull_request_id=?", repoID, pullRequestID).And(builder.In("status", PendingStatuses())).Find(&runs); err != nil {
		return nil, err
	}
	return runs, nil
}

// Inserts a run and its jobs.
// The title will be cut off at 255 characters if it's longer than 255 characters.
func InsertRunWithoutNotification(ctx context.Context, run *ActionRun, jobs []*jobparser.SingleWorkflow) error {
	return db.WithTx(ctx, func(ctx context.Context) error {
		index, err := db.GetNextResourceIndex(ctx, "action_run_index", run.RepoID)
		if err != nil {
			return err
		}
		run.Index = index
		run.Title, _ = util.SplitStringAtByteN(run.Title, 255)

		if err := db.Insert(ctx, run); err != nil {
			return err
		}

		if run.Repo == nil {
			repo, err := repo_model.GetRepositoryByID(ctx, run.RepoID)
			if err != nil {
				return err
			}
			run.Repo = repo
		}

		clearRepoRunCountCache(ctx, run.Repo)

		return InsertRunJobs(ctx, run, jobs)
	})
}

// Adds `ActionRunJob` instances from `SingleWorkflows` to an existing ActionRun.
func InsertRunJobs(ctx context.Context, run *ActionRun, jobs []*jobparser.SingleWorkflow) error {
	runJobs := make([]*ActionRunJob, 0, len(jobs))
	var hasWaiting bool
	for _, v := range jobs {
		id, job := v.Job()
		status := StatusFailure
		payload := []byte{}
		needs := []string{}
		name := run.Title
		runsOn := []string{}
		if job != nil {
			needs = job.Needs()
			if err := v.SetJob(id, job.EraseNeeds()); err != nil {
				return err
			}
			payload, _ = v.Marshal()

			if len(needs) > 0 || run.NeedApproval || v.IncompleteMatrix || v.IncompleteRunsOn || v.IncompleteWith {
				status = StatusBlocked
			} else if ifPassed, err := job.EvaluateIf(); err == nil && !ifPassed {
				log.Trace("job %q skipped by server-side 'if' evaluation", id)
				status = StatusSkipped
			} else {
				if err != nil && !errors.Is(err, jobparser.ErrCannotEvaluateInJobParser) {
					return fmt.Errorf("unable to evaluate job 'if' on server-side with unexpected error: %w", err)
				}
				status = StatusWaiting
				hasWaiting = true
			}

			name, _ = util.SplitStringAtByteN(job.Name, 255)
			runsOn = job.RunsOn()
		}

		runJob := &ActionRunJob{
			RunID:             run.ID,
			RepoID:            run.RepoID,
			OwnerID:           run.OwnerID,
			CommitSHA:         run.CommitSHA,
			IsForkPullRequest: run.IsForkPullRequest,
			Name:              name,
			WorkflowPayload:   payload,
			JobID:             id,
			Needs:             needs,
			RunsOn:            runsOn,
		}
		if err := runJob.PrepareNextAttempt(status); err != nil {
			return err
		}

		runJobs = append(runJobs, runJob)
	}

	if len(runJobs) > 0 {
		if err := db.Insert(ctx, runJobs); err != nil {
			return err
		}
	}

	// if there is a job in the waiting status, increase tasks version.
	if hasWaiting {
		if err := IncreaseTaskVersion(ctx, run.OwnerID, run.RepoID); err != nil {
			return err
		}
	}

	return nil
}

func GetLatestRun(ctx context.Context, repoID int64) (*ActionRun, error) {
	var run ActionRun
	has, err := db.GetEngine(ctx).Where("repo_id=?", repoID).OrderBy("id DESC").Limit(1).Get(&run)
	if err != nil {
		return nil, err
	} else if !has {
		return nil, fmt.Errorf("latest run: %w", util.ErrNotExist)
	}
	return &run, nil
}

func GetRunBefore(ctx context.Context, _ *ActionRun) (*ActionRun, error) {
	// TODO return the most recent run related to the run given in argument
	// see https://codeberg.org/forgejo/user-research/issues/63 for context
	return nil, util.ErrNotExist
}

func GetLatestRunForBranchAndWorkflow(ctx context.Context, repoID int64, branch, workflowFile, event string) (*ActionRun, error) {
	var run ActionRun
	q := db.GetEngine(ctx).Where("repo_id=?", repoID).And("workflow_id=?", workflowFile)
	if event != "" {
		q = q.And("event=?", event)
	}
	if branch != "" {
		q = q.And("ref=?", branch)
	}
	has, err := q.Desc("id").Get(&run)
	if err != nil {
		return nil, err
	} else if !has {
		return nil, util.NewNotExistErrorf("run with repo_id %d, ref %s, event %s, workflow_id %s", repoID, branch, event, workflowFile)
	}
	return &run, nil
}

func GetRunByID(ctx context.Context, id int64) (*ActionRun, error) {
	var run ActionRun
	has, err := db.GetEngine(ctx).Where("id=?", id).Get(&run)
	if err != nil {
		return nil, err
	}
	if !has {
		return nil, fmt.Errorf("run with id %d: %w", id, util.ErrNotExist)
	}

	return &run, nil
}

func GetRunByIndex(ctx context.Context, repoID, index int64) (*ActionRun, error) {
	run := &ActionRun{
		RepoID: repoID,
		Index:  index,
	}
	has, err := db.GetEngine(ctx).Get(run)
	if err != nil {
		return nil, err
	} else if !has {
		return nil, fmt.Errorf("run with index %d %d: %w", repoID, index, util.ErrNotExist)
	}

	return run, nil
}

// GetQueuedRunsByRepoID returns all workflow runs that belong to the given repository and whose status is either
// StatusWaiting or StatusBlocked.
func GetQueuedRunsByRepoID(ctx context.Context, repoID int64) ([]*ActionRun, error) {
	query := db.GetEngine(ctx).
		Where("repo_id=?", repoID).
		In("status", []Status{StatusWaiting, StatusBlocked}).
		Asc("id")

	var runs []*ActionRun
	if err := query.Find(&runs); err != nil {
		return nil, fmt.Errorf("cannot get queued workflow runs of repository %d: %w", repoID, err)
	}
	return runs, nil
}

// Error returned when ActionRun's optimistic concurrency control has indicated that the record has been updated in the
// database by another session since it was loaded in-memory in this session.
var ErrActionRunOutOfDate = errors.New("run has changed")

// UpdateRun updates a run.
// It requires the inputted run has Version set.
// It will return error if the version is not matched (it means the run has been changed after loaded).
// All calls to UpdateRunWithoutNotification that change run.Status from a not done status to a done status must call the ActionRunNowDone notification channel.
// Use the wrapper function UpdateRun instead.
func UpdateRunWithoutNotification(ctx context.Context, run *ActionRun, cols ...string) error {
	sess := db.GetEngine(ctx).ID(run.ID)
	if len(cols) > 0 {
		sess.Cols(cols...)
	}
	run.Title, _ = util.SplitStringAtByteN(run.Title, 255)
	affected, err := sess.Update(run)
	if err != nil {
		return err
	}
	if affected == 0 {
		// UPDATE has no conditions on it, and we never delete runs, so the only possible cause of this is
		// `xorm:"version"` tagged field indicated that the version has changed since the record was loaded.
		return ErrActionRunOutOfDate
	}

	if run.Status != 0 || slices.Contains(cols, "status") {
		if run.RepoID == 0 {
			run, err = GetRunByID(ctx, run.ID)
			if err != nil {
				return err
			}
		}
		if run.Repo == nil {
			repo, err := repo_model.GetRepositoryByID(ctx, run.RepoID)
			if err != nil {
				return err
			}
			run.Repo = repo
		}
		clearRepoRunCountCache(ctx, run.Repo)
	}

	return nil
}

// Performs the same computation as [ComputeExistingRunStatus] from a run ID, and returning the run. The caller is
// responsible for then invoking [actions_service.UpdateRun] for an update with notifications, or
// [actions_model.UpdateRunWithoutNotification] if notifications are already handled.
func ComputeRunStatus(ctx context.Context, runID int64) (run *ActionRun, columns []string, err error) {
	run, err = GetRunByID(ctx, runID)
	if err != nil {
		return nil, nil, err
	}
	columns, err = ComputeExistingRunStatus(ctx, run)
	return run, columns, err
}

// Compute the Status, Started, and Stopped fields of an ActionRun based upon the current job state within the run. The
// provided [ActionRun] is modified in-memory, but not in the database. The caller is responsible for then invoking
// [actions_service.UpdateRun] for an update with notifications, or [actions_model.UpdateRunWithoutNotification] if
// notifications are already handled.
func ComputeExistingRunStatus(ctx context.Context, run *ActionRun) (columns []string, err error) {
	jobs, err := GetRunJobsByRunID(ctx, run.ID)
	if err != nil {
		return nil, err
	}

	newStatus := AggregateJobStatus(jobs)
	if run.Status != newStatus {
		run.Status = newStatus
		columns = append(columns, "status")
	}
	if run.Started.IsZero() && run.Status.IsRunning() {
		run.Started = timeutil.TimeStampNow()
		columns = append(columns, "started")
	}
	if run.Stopped.IsZero() && run.Status.IsDone() {
		run.Stopped = timeutil.TimeStampNow()
		columns = append(columns, "stopped")
	}

	return columns, nil
}

// DeleteRun removes the given run. It is the caller's responsibility to handle the run's dependencies like artifacts or
// jobs. Nothing happens if the run does not exist.
func DeleteRun(ctx context.Context, runID int64) error {
	_, err := db.GetEngine(ctx).Delete(&ActionRun{ID: runID})
	return err
}

type ActionRunIndex db.ResourceIndex
