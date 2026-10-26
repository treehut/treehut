// Copyright 2025 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package actions

import (
	"context"
	"errors"
	"fmt"

	actions_model "forgejo.org/models/actions"
	secret_model "forgejo.org/models/secret"
	actions_module "forgejo.org/modules/actions"

	"code.forgejo.org/forgejo/runner/v13/act/jobparser"
)

func getSecretsOfTask(ctx context.Context, task *actions_model.ActionTask) (map[string]string, error) {
	secrets, err := getSecretsOfJob(ctx, task.Job)
	secrets["GITHUB_TOKEN"] = task.Token
	secrets["GITEA_TOKEN"] = task.Token
	secrets["FORGEJO_TOKEN"] = task.Token
	return secrets, err
}

func getSecretsOfJob(ctx context.Context, job *actions_model.ActionRunJob) (map[string]string, error) {
	isInnerWorkflowCall, err := job.IsWorkflowCallInnerJob()
	if err != nil {
		return nil, err
	}

	err = job.LoadRun(ctx)
	if err != nil {
		return nil, fmt.Errorf("failure to load job run: %w", err)
	}

	if isInnerWorkflowCall {
		return getSecretsOfInnerWorkflowCall(ctx, job)
	}

	if canAccessSecrets, err := job.CanAccessSecrets(ctx); err != nil {
		return nil, fmt.Errorf("failed evaluating CanAccessSecrets: %w", err)
	} else if !canAccessSecrets {
		// Ignore secrets for this run, (except the automatic GITHUB_TOKEN, GITEA_TOKEN and FORGEJO_TOKEN, which aren't
		// `getSecretsOfJob`'s responsibility)
		return map[string]string{}, nil
	}

	err = job.Run.LoadRepo(ctx)
	if err != nil {
		return nil, err
	}

	jobSecrets, err := secret_model.FetchActionSecrets(ctx, job.Run.Repo.OwnerID, job.Run.RepoID)
	if err != nil {
		// Don't return error details, just in case they contain confidential details and error reaches a user;
		// FetchActionSecrets logs all errors to the server log.
		return nil, errors.New("failure to fetch secrets")
	}
	return jobSecrets, nil
}

func getSecretsOfInnerWorkflowCall(ctx context.Context, job *actions_model.ActionRunJob) (map[string]string, error) {
	// Workflow calls can have two different behaviours -- they can either have `secrets: inherit` in which case we get
	// the secrets of the caller and pass them in, or, they can have `secrets: { ... }` with key-values that need to be
	// evaluated in the context of the parent (that is, `${{ secret.example_secret }}` would reference `example_secret`
	// from the caller's secrets).
	//
	// In either case, we need the caller job's secrets, and we need the caller job's workflow definition to find out
	// how they wanted secrets defined for this workflow call.
	outerWorkflowCall, err := job.Run.FindOuterWorkflowCall(ctx, job)
	if err != nil {
		return nil, fmt.Errorf("failure to find outer workflow call: %w", err)
	}
	outerSecrets, err := getSecretsOfJob(ctx, outerWorkflowCall)
	if err != nil {
		return nil, err
	}

	outerWorkflowPayload, err := outerWorkflowCall.DecodeWorkflowPayload()
	if err != nil {
		return nil, err
	}
	_, outerJob := outerWorkflowPayload.Job()
	if outerJob.InheritSecrets() {
		return outerSecrets, nil
	}

	// Gather all the data that is needed to perform an expression evaluation of the parent job's secrets context:
	err = outerWorkflowCall.LoadRun(ctx)
	if err != nil {
		return nil, fmt.Errorf("failure to load job's run: %w", err)
	}
	err = outerWorkflowCall.Run.LoadRepo(ctx)
	if err != nil {
		return nil, fmt.Errorf("failure to load run's repo: %w", err)
	}
	githubContext := generateGiteaContextForRun(outerWorkflowCall.Run)
	taskNeeds, err := FindTaskNeeds(ctx, outerWorkflowCall)
	if err != nil {
		return nil, fmt.Errorf("failure evaluating 'needs' for job: %w", err)
	}
	needs := make([]string, 0, len(taskNeeds))
	jobResults := make(map[string]string, len(taskNeeds))
	jobOutputs := make(map[string]map[string]string, len(taskNeeds))
	for jobID, n := range taskNeeds {
		qualified := string(jobID.ToLocal(job.JobNamespace))
		needs = append(needs, qualified)
		jobResults[qualified] = n.Result.String()
		jobOutputs[qualified] = n.Outputs
	}
	vars, err := actions_model.GetVariablesOfRun(ctx, job.Run)
	if err != nil {
		return nil, fmt.Errorf("failure evaluating 'vars' for run: %w", err)
	}

	var inputs map[string]any
	if outerWorkflowCall.Run.TriggerEvent == actions_module.GithubEventWorkflowDispatch {
		inputs = getRunInputs(outerWorkflowCall.Run)
	}

	jobSecrets := jobparser.EvaluateWorkflowCallSecrets(&jobparser.EvaluateWorkflowCallSecretsArgs{
		CallerWorkflow: outerWorkflowPayload,
		CallerSecrets:  outerSecrets,

		GitCtx:     githubContext,
		Vars:       vars,
		Needs:      needs,
		JobResults: jobResults,
		JobOutputs: jobOutputs,
		JobInputs:  inputs,
	})

	return jobSecrets, nil
}
