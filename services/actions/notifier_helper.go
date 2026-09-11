// Copyright 2022 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	actions_model "forgejo.org/models/actions"
	"forgejo.org/models/db"
	issues_model "forgejo.org/models/issues"
	packages_model "forgejo.org/models/packages"
	access_model "forgejo.org/models/perm/access"
	repo_model "forgejo.org/models/repo"
	unit_model "forgejo.org/models/unit"
	user_model "forgejo.org/models/user"
	actions_module "forgejo.org/modules/actions"
	"forgejo.org/modules/git"
	"forgejo.org/modules/gitrepo"
	"forgejo.org/modules/json"
	"forgejo.org/modules/log"
	"forgejo.org/modules/optional"
	"forgejo.org/modules/setting"
	api "forgejo.org/modules/structs"
	"forgejo.org/modules/util"
	webhook_module "forgejo.org/modules/webhook"
	"forgejo.org/services/convert"

	"code.forgejo.org/forgejo/runner/v12/act/jobparser"
	"code.forgejo.org/forgejo/runner/v12/act/model"
)

type methodCtx struct{}

var methodCtxKey = methodCtx{}

// withMethod sets the notification method that this context currently executes.
// Used for debugging/ troubleshooting purposes.
func withMethod(ctx context.Context, method string) context.Context {
	// don't overwrite
	if v := ctx.Value(methodCtxKey); v != nil {
		if _, ok := v.(string); ok {
			return ctx
		}
	}
	return context.WithValue(ctx, methodCtxKey, method)
}

// getMethod gets the notification method that this context currently executes.
// Default: "notify"
// Used for debugging/ troubleshooting purposes.
func getMethod(ctx context.Context) string {
	if v := ctx.Value(methodCtxKey); v != nil {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return "notify"
}

type NotifyInput struct {
	// required
	Repo  *repo_model.Repository
	Doer  *user_model.User
	Event webhook_module.HookEventType

	// optional
	Ref         git.RefName
	Commit      string
	Payload     api.Payloader
	PullRequest *issues_model.PullRequest
}

func NewNotifyInput(repo *repo_model.Repository, doer *user_model.User, event webhook_module.HookEventType) *NotifyInput {
	return &NotifyInput{
		Repo:  repo,
		Doer:  doer,
		Event: event,
	}
}

func NewNotifyInputForSchedules(repo *repo_model.Repository) *NotifyInput {
	// the doer here will be ignored as we force using action user when handling schedules
	return NewNotifyInput(repo, user_model.NewActionsUser(), webhook_module.HookEventSchedule)
}

func (input *NotifyInput) WithDoer(doer *user_model.User) *NotifyInput {
	input.Doer = doer
	return input
}

func (input *NotifyInput) WithRef(ref string) *NotifyInput {
	input.Ref = git.RefName(ref)
	return input
}

func (input *NotifyInput) WithCommit(commitID string) *NotifyInput {
	input.Commit = commitID
	return input
}

func (input *NotifyInput) WithPayload(payload api.Payloader) *NotifyInput {
	input.Payload = payload
	return input
}

// for cases like issue comments on PRs, which have the PR data, but don't run on its ref
func (input *NotifyInput) WithPullRequestData(pr *issues_model.PullRequest) *NotifyInput {
	input.PullRequest = pr
	return input
}

func (input *NotifyInput) WithPullRequest(pr *issues_model.PullRequest) *NotifyInput {
	input.PullRequest = pr
	if input.Ref == "" {
		input.Ref = git.RefName(pr.GetGitRefName())
	}
	return input
}

func (input *NotifyInput) Notify(ctx context.Context) {
	log.Trace("execute %v for event %v whose doer is %v", getMethod(ctx), input.Event, input.Doer.Name)

	if err := Notify(ctx, input); err != nil {
		log.Error("an error occurred while executing the %s actions method: %v", getMethod(ctx), err)
	}
}

var Notify = func(ctx context.Context, input *NotifyInput) error {
	shouldDetectSchedules := input.Event == webhook_module.HookEventPush && input.Ref.BranchName() == input.Repo.DefaultBranch
	if input.Doer.IsActions() {
		// avoiding triggering cyclically, for example:
		// a comment of an issue will trigger the runner to add a new comment as reply,
		// and the new comment will trigger the runner again.
		log.Debug("ignore executing %v for event %v whose doer is %v", getMethod(ctx), input.Event, input.Doer.Name)

		// we should update schedule tasks in this case, because
		//   1. schedule tasks cannot be triggered by other events, so cyclic triggering will not occur
		//   2. some schedule tasks may update the repo periodically, so the refs of schedule tasks need to be updated
		if shouldDetectSchedules {
			return DetectAndHandleSchedules(ctx, input.Repo)
		}

		return nil
	}
	if input.Repo.IsEmpty || input.Repo.IsArchived {
		return nil
	}
	if unit_model.TypeActions.UnitGlobalDisabled() {
		if err := CleanRepoScheduleTasks(ctx, input.Repo, true); err != nil {
			log.Error("CleanRepoScheduleTasks: %v", err)
		}
		return nil
	}
	if err := input.Repo.LoadUnits(ctx); err != nil {
		return fmt.Errorf("repo.LoadUnits: %w", err)
	} else if !input.Repo.UnitEnabled(ctx, unit_model.TypeActions) {
		return nil
	}

	gitRepo, commit, ref, err := getGitRepoAndCommit(ctx, input)
	if err != nil {
		return err
	} else if gitRepo == nil && commit == nil {
		return nil
	}
	defer gitRepo.Close()

	logger.Debug("Triggering workflow detection and workflows for commit %q (input Git ref: %q), event %s",
		commit.ID, input.Ref, input.Event)

	if skipWorkflows(input, commit) {
		return nil
	}

	if SkipPullRequestEvent(ctx, input.Event, input.Repo.ID, commit.ID.String()) {
		log.Trace("repo %s with commit %s skip event %v", input.Repo.RepoPath(), commit.ID, input.Event)
		return nil
	}

	detectedWorkflows, schedules, err := detectWorkflows(ctx, input, gitRepo, commit, shouldDetectSchedules)
	if err != nil {
		return err
	}

	if shouldDetectSchedules {
		if err := handleSchedules(ctx, schedules, commit, input); err != nil {
			return err
		}
	}

	return handleWorkflows(ctx, detectedWorkflows, commit, input, ref.String())
}

func getGitRepoAndCommit(ctx context.Context, input *NotifyInput) (*git.Repository, *git.Commit, git.RefName, error) {
	gitRepo, err := gitrepo.OpenRepository(ctx, input.Repo)
	if err != nil {
		return nil, nil, "", fmt.Errorf("git.OpenRepository: %w", err)
	}

	ref := input.Ref
	if ref.BranchName() != input.Repo.DefaultBranch && actions_module.IsDefaultBranchWorkflow(input.Event) {
		if ref != "" {
			log.Warn("Event %q should only trigger workflows on the default branch, but its ref is %q. Will fall back to the default branch",
				input.Event, ref)
		}
		ref = git.RefNameFromBranch(input.Repo.DefaultBranch)
	}
	if ref == "" {
		log.Warn("Ref of event %q is empty, will fall back to the default branch", input.Event)
		ref = git.RefNameFromBranch(input.Repo.DefaultBranch)
	}

	commitID := input.Commit
	if commitID == "" {
		if commitID, err = gitRepo.GetRefCommitID(ref.String()); err != nil {
			gitRepo.Close()
			return nil, nil, "", fmt.Errorf("gitRepo.GetRefCommitID: %w", err)
		}
	}

	// Get the commit object for the ref
	commit, err := gitRepo.GetCommit(commitID)
	if err != nil {
		gitRepo.Close()
		return nil, nil, "", fmt.Errorf("gitRepo.GetCommit: %w", err)
	}
	return gitRepo, commit, ref, nil
}

func detectWorkflows(ctx context.Context, input *NotifyInput, gitRepo *git.Repository, commit *git.Commit, shouldDetectSchedules bool) ([]*actions_module.DetectedWorkflow, []*actions_module.DetectedWorkflow, error) {
	var detectedWorkflows []*actions_module.DetectedWorkflow
	actionsConfig := input.Repo.MustGetUnit(ctx, unit_model.TypeActions).ActionsConfig()
	workflows, schedules, err := actions_module.DetectWorkflows(gitRepo, commit,
		input.Event,
		input.Payload,
		shouldDetectSchedules,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("DetectWorkflows: %w", err)
	}

	log.Trace("repo %s with commit %s event %s find %d workflows and %d schedules",
		input.Repo.RepoPath(),
		commit.ID,
		input.Event,
		len(workflows),
		len(schedules),
	)

	if input.PullRequest != nil && !actions_module.IsDefaultBranchWorkflow(input.Event) {
		// detect pull_request_target workflows
		baseRef := git.BranchPrefix + input.PullRequest.BaseBranch
		baseCommit, err := gitRepo.GetCommit(baseRef)
		if err != nil {
			if prp, ok := input.Payload.(*api.PullRequestPayload); ok && errors.Is(err, util.ErrNotExist) {
				// the baseBranch was deleted and the PR closed: the action can be skipped
				if prp.Action == api.HookIssueClosed {
					return nil, nil, nil
				}
			}
			return nil, nil, fmt.Errorf("gitRepo.GetCommit: %w", err)
		}
		baseWorkflows, _, err := actions_module.DetectWorkflows(gitRepo, baseCommit, input.Event, input.Payload, false)
		if err != nil {
			return nil, nil, fmt.Errorf("DetectWorkflows: %w", err)
		}
		if len(baseWorkflows) == 0 {
			log.Trace("repo %s with commit %s couldn't find pull_request_target workflows", input.Repo.RepoPath(), baseCommit.ID)
		} else {
			for _, wf := range baseWorkflows {
				if wf.TriggerEvent.Name == actions_module.GithubEventPullRequestTarget {
					detectedWorkflows = append(detectedWorkflows, wf)
				}
			}
		}

		useHeadOrBaseCommit, pullRequestNeedApproval, err := getPullRequestCommitAndApproval(ctx, input.PullRequest, input.Doer, input.Event)
		if err != nil {
			return nil, nil, fmt.Errorf("getPullRequestTrust: %w", err)
		}

		if useHeadOrBaseCommit == useBaseCommit {
			workflows = baseWorkflows
		} else if pullRequestNeedApproval {
			for _, wf := range workflows {
				wf.NeedApproval = pullRequestNeedApproval
			}
		}
	}

	for _, wf := range workflows {
		if actionsConfig.IsWorkflowDisabled(wf.EntryName) {
			log.Trace("repo %s has disable workflows %s", input.Repo.RepoPath(), wf.EntryName)
			continue
		}

		if wf.TriggerEvent.Name != actions_module.GithubEventPullRequestTarget {
			detectedWorkflows = append(detectedWorkflows, wf)
		}
	}

	return detectedWorkflows, schedules, nil
}

func SkipPullRequestEvent(ctx context.Context, event webhook_module.HookEventType, repoID int64, commitSHA string) bool {
	if event != webhook_module.HookEventPullRequestSync {
		return false
	}

	run := actions_model.ActionRun{
		Event:     webhook_module.HookEventPullRequest,
		RepoID:    repoID,
		CommitSHA: commitSHA,
	}
	exist, err := db.GetEngine(ctx).Exist(&run)
	if err != nil {
		log.Error("Exist ActionRun %v: %v", run, err)
		return false
	}
	return exist
}

func skipWorkflows(input *NotifyInput, commit *git.Commit) bool {
	// skip workflow runs with a configured skip-ci string in commit message or pr title if the event is push or pull_request(_sync)
	// https://docs.github.com/en/actions/managing-workflow-runs/skipping-workflow-runs
	skipWorkflowEvents := []webhook_module.HookEventType{
		webhook_module.HookEventPush,
		webhook_module.HookEventPullRequest,
		webhook_module.HookEventPullRequestSync,
	}
	if slices.Contains(skipWorkflowEvents, input.Event) {
		for _, s := range setting.Actions.SkipWorkflowStrings {
			if input.PullRequest != nil && strings.Contains(input.PullRequest.Issue.Title, s) {
				log.Debug("repo %s: skipped run for pr %v because of %s string", input.Repo.RepoPath(), input.PullRequest.Issue.ID, s)
				return true
			}
			if strings.Contains(commit.CommitMessage, s) {
				log.Debug("repo %s with commit %s: skipped run because of %s string", input.Repo.RepoPath(), commit.ID, s)
				return true
			}
		}
	}
	return false
}

func handleWorkflows(
	ctx context.Context,
	detectedWorkflows []*actions_module.DetectedWorkflow,
	commit *git.Commit,
	input *NotifyInput,
	ref string,
) error {
	if len(detectedWorkflows) == 0 {
		log.Trace("repo %s with commit %s couldn't find workflows", input.Repo.RepoPath(), commit.ID)
		return nil
	}

	p, err := json.Marshal(input.Payload)
	if err != nil {
		return fmt.Errorf("json.Marshal: %w", err)
	}

	for _, dwf := range detectedWorkflows {
		run := &actions_model.ActionRun{
			Title:             strings.SplitN(commit.CommitMessage, "\n", 2)[0],
			RepoID:            input.Repo.ID,
			OwnerID:           input.Repo.OwnerID,
			WorkflowID:        dwf.EntryName,
			WorkflowDirectory: dwf.EntryDirectory,
			TriggerUserID:     input.Doer.ID,
			Ref:               ref,
			CommitSHA:         commit.ID.String(),
			Event:             input.Event,
			EventPayload:      string(p),
			TriggerEvent:      dwf.TriggerEvent.Name,
			Status:            actions_model.StatusWaiting,
		}
		workflowSourceCommit := commit
		if dwf.SourceCommit != nil && dwf.SourceCommit.ID.String() != commit.ID.String() {
			run.WorkflowSourceCommit = optional.Some(dwf.SourceCommit.ID.String())
			workflowSourceCommit = dwf.SourceCommit
		}

		if !actions_module.IsDefaultBranchWorkflow(input.Event) {
			if err := setRunTrustForPullRequest(ctx, run, input.PullRequest, dwf.NeedApproval); err != nil {
				return fmt.Errorf("setTrustForPullRequest: %w", err)
			}
		}

		workflow, err := model.ReadWorkflow(bytes.NewReader(dwf.Content), false)
		if err != nil {
			log.Error("unable to read workflow: %v", err)
		}

		notifications, err := workflow.Notifications()
		if err != nil {
			log.Error("Notifications: %w", err)
		}
		run.NotifyEmail = notifications

		if err := run.LoadAttributes(ctx); err != nil {
			log.Error("LoadAttributes: %v", err)
			continue
		}

		vars, err := actions_model.GetVariablesOfRun(ctx, run)
		if err != nil {
			log.Error("GetVariablesOfRun: %v", err)
			continue
		}

		err = ConfigureActionRunConcurrency(workflow, run, vars, map[string]any{})
		if err != nil {
			log.Error("ConfigureActionRunConcurrency: %v", err)
		}

		var jobs []*jobparser.SingleWorkflow
		var errorCode actions_model.PreExecutionError
		var errorDetails []any
		if dwf.EventDetectionError != nil { // don't even bother trying to parse jobs due to event detection error
			errorCode = actions_model.ErrorCodeEventDetectionError
			errorDetails = []any{dwf.EventDetectionError.Error()}
			run.Status = actions_model.StatusFailure
			jobs = []*jobparser.SingleWorkflow{{
				Name: dwf.EntryName,
			}}
		} else {
			jobs, err = actions_module.JobParser(dwf.Content,
				jobparser.WithVars(vars),
				// We don't have any job outputs yet, but `WithJobOutputs(...)` triggers JobParser to supporting its
				// `IncompleteMatrix` tagging for any jobs that require the inputs of other jobs.
				jobparser.WithJobOutputs(map[string]map[string]string{}),
				jobparser.SupportIncompleteRunsOn(),
				jobparser.ExpandLocalReusableWorkflows(expandLocalReusableWorkflows(workflowSourceCommit)),
				jobparser.ExpandInstanceReusableWorkflows(expandInstanceReusableWorkflows(ctx)),
				jobparser.WithGitContext(generateGiteaContextForRun(run)),
			)
			if err != nil {
				log.Info("jobparser.Parse: invalid workflow, setting job status to failed: %v", err)
				errorCode = actions_model.ErrorCodeJobParsingError
				errorDetails = []any{err.Error()}
				run.Status = actions_model.StatusFailure
				jobs = []*jobparser.SingleWorkflow{{
					Name: dwf.EntryName,
				}}
			}
		}

		if run.ConcurrencyType == actions_model.CancelInProgress {
			if err := CancelPreviousWithConcurrencyGroup(
				ctx,
				run.RepoID,
				run.ConcurrencyGroup,
			); err != nil {
				log.Error("CancelPreviousWithConcurrencyGroup: %v", err)
			}
		}

		err = db.WithTx(ctx, func(ctx context.Context) error {
			// Transaction avoids any chance of a run being picked up in a Waiting state when we're about to put it into
			// a PreExecutionError a millisecond later.
			if err := InsertRun(ctx, run, jobs); err != nil {
				return fmt.Errorf("InsertRun: %w", err)
			}
			if errorCode != 0 {
				if err := FailRunPreExecutionError(ctx, run, errorCode, errorDetails); err != nil {
					return fmt.Errorf("FailRunPreExecutionError: %w", err)
				}
			}
			return nil
		})
		if err != nil {
			log.Error("InsertRun: %v", err)
			continue
		}

		alljobs, err := db.Find[actions_model.ActionRunJob](ctx, actions_model.FindRunJobOptions{RunID: run.ID})
		if err != nil {
			log.Error("FindRunJobs: %v", err)
			continue
		}
		CreateCommitStatus(ctx, alljobs...)

		if err := consistencyCheckRun(ctx, run); err != nil {
			log.Error("SanityCheckRun: %v", err)
			continue
		}
	}
	return nil
}

func newNotifyInputFromIssue(issue *issues_model.Issue, event webhook_module.HookEventType) *NotifyInput {
	return NewNotifyInput(issue.Repo, issue.Poster, event)
}

func notifyRelease(ctx context.Context, doer *user_model.User, rel *repo_model.Release, action api.HookReleaseAction) {
	if err := rel.LoadAttributes(ctx); err != nil {
		log.Error("LoadAttributes: %v", err)
		return
	}

	permission, _ := access_model.GetUserRepoPermission(ctx, rel.Repo, doer)

	NewNotifyInput(rel.Repo, doer, webhook_module.HookEventRelease).
		WithRef(git.RefNameFromTag(rel.TagName).String()).
		WithPayload(&api.ReleasePayload{
			Action:     action,
			Release:    convert.ToAPIRelease(ctx, rel.Repo, rel, false),
			Repository: convert.ToRepo(ctx, rel.Repo, permission),
			Sender:     convert.ToUser(ctx, doer, nil),
		}).
		Notify(ctx)
}

func notifyPackage(ctx context.Context, sender *user_model.User, pd *packages_model.PackageDescriptor, action api.HookPackageAction) {
	if pd.Repository == nil {
		// When a package is uploaded to an organization, it could trigger an event to notify.
		// So the repository could be nil, however, actions can't support that yet.
		// See https://github.com/go-gitea/gitea/pull/17940
		return
	}

	apiPackage, err := convert.ToPackage(ctx, pd, sender)
	if err != nil {
		log.Error("Error converting package: %v", err)
		return
	}

	NewNotifyInput(pd.Repository, sender, webhook_module.HookEventPackage).
		WithPayload(&api.PackagePayload{
			Action:  action,
			Package: apiPackage,
			Sender:  convert.ToUser(ctx, sender, nil),
		}).
		Notify(ctx)
}

func handleSchedules(
	ctx context.Context,
	detectedWorkflows []*actions_module.DetectedWorkflow,
	commit *git.Commit,
	input *NotifyInput,
) error {
	if input.Ref.BranchName() != input.Repo.DefaultBranch {
		log.Trace("commit branch is not default branch in repo")
		return nil
	}

	if count, err := db.Count[actions_model.ActionSchedule](ctx, actions_model.FindScheduleOptions{RepoID: input.Repo.ID}); err != nil {
		log.Error("CountSchedules: %v", err)
		return err
	} else if count > 0 {
		if err := CleanRepoScheduleTasks(ctx, input.Repo, false); err != nil {
			log.Error("CleanRepoScheduleTasks: %v", err)
		}
	}

	if len(detectedWorkflows) == 0 {
		log.Trace("repo %s with commit %s couldn't find schedules", input.Repo.RepoPath(), commit.ID)
		return nil
	}

	payload := &api.SchedulePayload{
		Action: api.HookScheduleCreated,
	}

	p, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("json.Marshal: %w", err)
	}

	crons := make([]*actions_model.ActionSchedule, 0, len(detectedWorkflows))
	for _, dwf := range detectedWorkflows {
		// Check cron job condition. Only working in default branch
		workflow, err := model.ReadWorkflow(bytes.NewReader(dwf.Content), false)
		if err != nil {
			log.Error("ReadWorkflow: %v", err)
			continue
		}
		schedules := workflow.OnSchedule()
		if len(schedules) == 0 {
			log.Warn("no schedule event")
			continue
		}

		now := time.Now()
		specs := make([]*actions_model.ActionScheduleSpec, 0, len(schedules))
		for _, schedule := range schedules {
			scheduleSpec, err := actions_model.NewActionScheduleSpec(schedule.Cron, optional.FromNonDefault(schedule.TimeZone), now)
			if err != nil {
				return err
			}
			specs = append(specs, scheduleSpec)
		}

		title := workflow.Name
		if len(title) < 1 {
			title = dwf.GetWorkflowPath()
		}

		run := &actions_model.ActionSchedule{
			Title:             title,
			RepoID:            input.Repo.ID,
			OwnerID:           input.Repo.OwnerID,
			WorkflowID:        dwf.EntryName,
			WorkflowDirectory: dwf.EntryDirectory,
			TriggerUserID:     user_model.ActionsUserID,
			Ref:               input.Ref.String(),
			CommitSHA:         commit.ID.String(),
			Event:             input.Event,
			EventPayload:      string(p),
			Specs:             specs,
			Content:           dwf.Content,
		}
		crons = append(crons, run)
	}

	return actions_model.CreateScheduleTask(ctx, crons)
}

// DetectAndHandleSchedules detects the schedule workflows on the default branch and create schedule tasks
func DetectAndHandleSchedules(ctx context.Context, repo *repo_model.Repository) error {
	if repo.IsEmpty || repo.IsArchived {
		return nil
	}

	gitRepo, err := gitrepo.OpenRepository(ctx, repo)
	if err != nil {
		return fmt.Errorf("git.OpenRepository: %w", err)
	}
	defer gitRepo.Close()

	// Only detect schedule workflows on the default branch
	commit, err := gitRepo.GetCommit(repo.DefaultBranch)
	if err != nil {
		return fmt.Errorf("gitRepo.GetCommit: %w", err)
	}
	scheduleWorkflows, err := actions_module.DetectScheduledWorkflows(gitRepo, commit)
	if err != nil {
		return fmt.Errorf("detect schedule workflows: %w", err)
	}
	if len(scheduleWorkflows) == 0 {
		return nil
	}

	// We need a notifyInput to call handleSchedules
	// if repo is a mirror, commit author maybe an external user,
	// so we use action user as the Doer of the notifyInput
	notifyInput := NewNotifyInputForSchedules(repo).
		WithRef(git.RefNameFromBranch(repo.DefaultBranch).String())

	return handleSchedules(ctx, scheduleWorkflows, commit, notifyInput)
}
