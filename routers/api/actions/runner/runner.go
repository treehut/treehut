// Copyright 2022 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package runner

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	actions_model "forgejo.org/models/actions"
	repo_model "forgejo.org/models/repo"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/actions"
	"forgejo.org/modules/log"
	"forgejo.org/modules/sync"
	"forgejo.org/modules/util"
	actions_service "forgejo.org/services/actions"

	runnerv1 "code.forgejo.org/forgejo/actions-proto/runner/v1"
	"code.forgejo.org/forgejo/actions-proto/runner/v1/runnerv1connect"
	"connectrpc.com/connect"
	gouuid "github.com/google/uuid"
)

func NewRunnerServiceHandler() (string, http.Handler) {
	return runnerv1connect.NewRunnerServiceHandler(
		&Service{},
		connect.WithCompressMinBytes(1024),
		withRunner,
	)
}

var _ runnerv1connect.RunnerServiceClient = (*Service)(nil)

type Service struct {
	runnerv1connect.UnimplementedRunnerServiceHandler
	runnerRequestKeyMutexMap sync.MutexMap
}

// Register for new runner.
func (*Service) Register(
	ctx context.Context,
	req *connect.Request[runnerv1.RegisterRequest],
) (*connect.Response[runnerv1.RegisterResponse], error) {
	if req.Msg.Token == "" || req.Msg.Name == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("missing runner token, name"))
	}

	runnerToken, err := actions_model.GetRunnerToken(ctx, req.Msg.Token)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("runner registration token not found"))
	}

	if !runnerToken.IsActive {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("runner registration token has been invalidated, please use the latest one"))
	}

	if has, ownerID := runnerToken.OwnerID.Get(); has {
		if _, err := user_model.GetUserByID(ctx, ownerID); err != nil {
			return nil, connect.NewError(connect.CodeInternal, errors.New("owner of the token not found"))
		}
	}

	if has, repoID := runnerToken.RepoID.Get(); has {
		if _, err := repo_model.GetRepositoryByID(ctx, repoID); err != nil {
			return nil, connect.NewError(connect.CodeInternal, errors.New("repository of the token not found"))
		}
	}

	labels := req.Msg.Labels

	// create new runner
	name, _ := util.SplitStringAtByteN(req.Msg.Name, 255)
	runner := &actions_model.ActionRunner{
		UUID:        gouuid.New().String(),
		Name:        name,
		OwnerID:     runnerToken.OwnerID.ValueOrDefault(0),
		RepoID:      runnerToken.RepoID.ValueOrDefault(0),
		Version:     req.Msg.Version,
		AgentLabels: labels,
		Ephemeral:   req.Msg.Ephemeral,
	}
	runner.GenerateToken()

	// create new runner
	if err := actions_model.CreateRunner(ctx, runner); err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("can't create new runner"))
	}

	// update token status
	runnerToken.IsActive = true
	if err := actions_model.UpdateRunnerToken(ctx, runnerToken, "is_active"); err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("can't update runner token status"))
	}

	res := connect.NewResponse(&runnerv1.RegisterResponse{
		Runner: &runnerv1.Runner{
			Id:        runner.ID,
			Uuid:      runner.UUID,
			Token:     runner.Token,
			Name:      runner.Name,
			Version:   runner.Version,
			Labels:    runner.AgentLabels,
			Ephemeral: runner.Ephemeral,
		},
	})

	return res, nil
}

func (*Service) Declare(
	ctx context.Context,
	req *connect.Request[runnerv1.DeclareRequest],
) (*connect.Response[runnerv1.DeclareResponse], error) {
	runner := GetRunner(ctx)
	runner.AgentLabels = req.Msg.Labels
	runner.Version = req.Msg.Version
	if err := actions_model.UpdateRunner(ctx, runner, "agent_labels", "version"); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("update runner: %w", err))
	}

	return connect.NewResponse(&runnerv1.DeclareResponse{
		Runner: &runnerv1.Runner{
			Id:        runner.ID,
			Uuid:      runner.UUID,
			Token:     runner.Token,
			Name:      runner.Name,
			Version:   runner.Version,
			Labels:    runner.AgentLabels,
			Ephemeral: runner.Ephemeral,
		},
	}), nil
}

// FetchTask assigns a task to the runner
func (s *Service) FetchTask(
	ctx context.Context,
	req *connect.Request[runnerv1.FetchTaskRequest],
) (*connect.Response[runnerv1.FetchTaskResponse], error) {
	runner := GetRunner(ctx)

	requestKey := getRequestKey(ctx)
	if requestKey != nil {
		// It's possible for Forgejo to receive multiple concurrent requests for a given request key if the client made
		// a request (A), request (A) took longer than the client's HTTP timeout, request (A) continues to run on
		// Forgejo, and the client sends request (B).  In that case, we need to protect against reading from the
		// database and sending only *some* of the tasks for the request key back to the runner, as they get assigned
		// and committed to the database from request (A), but while request (A) is still running and request (B) is
		// received.  To do this, we lock on the request key with a MutexMap.
		//
		// The lock must be held for the entirety of `FetchTask`, so even if the request key isn't used to recover
		// tasks, the lock is held while new tasks are picked.
		locked, cleanup := s.runnerRequestKeyMutexMap.TryLock(*requestKey)
		defer cleanup()
		if !locked {
			// Another goroutine is currently processing some work for this request key.  Provide an error to the
			// client.  This will allow the client to retry with the same request key at its typical fetch interval.
			return nil, connect.NewError(connect.CodeInternal, errors.New("request key is currently locked; retry soon"))
		}

		recoveredTasks, err := recoverTasks(ctx, runner, *requestKey)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		} else if len(recoveredTasks) > 0 {
			resp := &runnerv1.FetchTaskResponse{
				Task:            recoveredTasks[0],
				TasksVersion:    0,
				AdditionalTasks: recoveredTasks[1:],
			}
			return connect.NewResponse(resp), nil
		}
	}

	latestVersion, err := getLatestTasksVersion(ctx, runner.OwnerID, runner.RepoID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	var task *runnerv1.Task
	var additionalTasks []*runnerv1.Task
	if req.Msg.TasksVersion != latestVersion {
		// if the task version in request is not equal to the version in db,
		// it means there may still be some tasks not be assigned.
		// try to pick a task for the runner that send the request.
		if t, err := actions_service.PickTask(ctx, runner, requestKey, nil); err != nil {
			if !actions_service.IsNoTaskAvailable(err) {
				log.Error("pick task failed: %v", err)
				return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("pick task: %w", err))
			}
		} else {
			task = t

			taskCapacity := req.Msg.GetTaskCapacity()
			taskCapacity-- // remove 1 for the task already fetched as `task`
			for taskCapacity > 0 {
				t, err := actions_service.PickTask(ctx, runner, requestKey, nil)
				if err != nil {
					if !actions_service.IsNoTaskAvailable(err) {
						// Don't return an error to the client/runner -- we've already assigned one-or-more tasks to the runner
						// and if we don't return them, they can't be picked up by another runner and will become zombie tasks.
						// Log the error and return the tasks we've assigned so far.
						log.Error("pick task failed: %v", err)
					}
					break
				}

				additionalTasks = append(additionalTasks, t)
				taskCapacity--
			}
		}
	}
	res := connect.NewResponse(&runnerv1.FetchTaskResponse{
		Task:            task,
		TasksVersion:    latestVersion,
		AdditionalTasks: additionalTasks,
	})
	return res, nil
}

func (*Service) FetchSingleTask(
	ctx context.Context,
	req *connect.Request[runnerv1.FetchSingleTaskRequest],
) (*connect.Response[runnerv1.FetchSingleTaskResponse], error) {
	runner := GetRunner(ctx)

	requestKey := getRequestKey(ctx)
	if requestKey != nil {
		recoveredTasks, err := recoverTasks(ctx, runner, *requestKey)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		} else if len(recoveredTasks) == 1 {
			resp := &runnerv1.FetchSingleTaskResponse{
				TasksVersion: 0,
				Task:         recoveredTasks[0],
			}
			return connect.NewResponse(resp), nil
		} else if len(recoveredTasks) > 1 {
			return nil, connect.NewError(connect.CodeInternal,
				fmt.Errorf("cannot recover %d tasks because runner requested only one", len(recoveredTasks)))
		}
	}

	latestVersion, err := getLatestTasksVersion(ctx, runner.OwnerID, runner.RepoID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	var task *runnerv1.Task
	if req.Msg.TasksVersion != latestVersion {
		var handle *string
		if req.Msg.Handle != nil && *req.Msg.Handle != "" {
			handle = req.Msg.Handle
		}

		if t, err := actions_service.PickTask(ctx, runner, requestKey, handle); err != nil {
			if !actions_service.IsNoTaskAvailable(err) {
				log.Error("pick task failed: %v", err)
				return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("pick task: %w", err))
			}
		} else {
			task = t
		}
	}
	res := connect.NewResponse(&runnerv1.FetchSingleTaskResponse{
		TasksVersion: latestVersion,
		Task:         task,
	})
	return res, nil
}

// UpdateTask updates the task status.
func (*Service) UpdateTask(
	ctx context.Context,
	req *connect.Request[runnerv1.UpdateTaskRequest],
) (*connect.Response[runnerv1.UpdateTaskResponse], error) {
	runner := GetRunner(ctx)

	task, err := actions_service.UpdateTaskByState(ctx, runner.ID, req.Msg.State)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("update task: %w", err))
	}

	for k, v := range req.Msg.Outputs {
		if len(k) > 255 {
			log.Warn("Ignore the output of task %d because the key is too long: %q", task.ID, k)
			continue
		}
		// The value can be a maximum of 1 MB
		if l := len(v); l > 1024*1024 {
			log.Warn("Ignore the output %q of task %d because the value is too long: %v", k, task.ID, l)
			continue
		}
		// There's another limitation on GitHub that the total of all outputs in a workflow run can be a maximum of 50 MB.
		// We don't check the total size here because it's not easy to do, and it doesn't really worth it.
		// See https://docs.github.com/en/actions/using-jobs/defining-outputs-for-jobs

		if err := actions_model.InsertTaskOutputIfNotExist(ctx, task.ID, k, v); err != nil {
			log.Warn("Failed to insert the output %q of task %d: %v", k, task.ID, err)
			// It's ok not to return errors, the runner will resend the outputs.
		}
	}
	sentOutputs, err := actions_model.FindTaskOutputKeyByTaskID(ctx, task.ID)
	if err != nil {
		log.Warn("Failed to find the sent outputs of task %d: %v", task.ID, err)
		// It's not to return errors, it can be handled when the runner resends sent outputs.
	}

	if err := task.LoadJob(ctx); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("load job: %w", err))
	}
	if err := task.Job.LoadRun(ctx); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("load run: %w", err))
	}

	// don't create commit status for cron job
	if task.Job.Run.ScheduleID == 0 {
		actions_service.CreateCommitStatus(ctx, task.Job)
	}

	if req.Msg.State.Result != runnerv1.Result_RESULT_UNSPECIFIED {
		if err := actions_service.EmitJobsIfReady(task.Job.RunID); err != nil {
			log.Error("Emit ready jobs of run %d: %v", task.Job.RunID, err)
		}
		if runner.Ephemeral {
			err := actions_model.DeleteRunner(ctx, runner)
			if err != nil {
				log.Error("failed to delete ephemeral runner %v, %w", task.RunnerID, err)
				return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to delete ephemeral runner %v, %w", task.RunnerID, err))
			}
		}
	}

	return connect.NewResponse(&runnerv1.UpdateTaskResponse{
		State: &runnerv1.TaskState{
			Id:     req.Msg.State.Id,
			Result: task.Status.AsResult(),
		},
		SentOutputs: sentOutputs,
	}), nil
}

// UpdateLog uploads log of the task.
func (*Service) UpdateLog(
	ctx context.Context,
	req *connect.Request[runnerv1.UpdateLogRequest],
) (*connect.Response[runnerv1.UpdateLogResponse], error) {
	runner := GetRunner(ctx)

	res := connect.NewResponse(&runnerv1.UpdateLogResponse{})

	task, err := actions_model.GetTaskByID(ctx, req.Msg.TaskId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("get task: %w", err))
	} else if runner.ID != task.RunnerID {
		return nil, connect.NewError(connect.CodeInternal, errors.New("invalid runner for task"))
	}

	ack := task.LogLength
	emptyRows := len(req.Msg.Rows) == 0 || req.Msg.Index > ack || int64(len(req.Msg.Rows))+req.Msg.Index <= ack
	updateTaskColumns := []string{} // columns for UpdateTask(...)

	if !emptyRows {
		if task.LogInStorage {
			return nil, connect.NewError(connect.CodeAlreadyExists, errors.New("log file has been archived"))
		}

		rows := req.Msg.Rows[ack-req.Msg.Index:]
		ns, err := actions.WriteLogs(ctx, task.LogFilename, task.LogSize, rows)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("write logs: %w", err))
		}
		task.LogLength += int64(len(rows))
		for _, n := range ns {
			task.LogIndexes = append(task.LogIndexes, task.LogSize)
			task.LogSize += int64(n)
		}

		res.Msg.AckIndex = task.LogLength
		updateTaskColumns = append(updateTaskColumns, "log_indexes", "log_length", "log_size")
	} else {
		res.Msg.AckIndex = ack
	}

	var remove func()
	if req.Msg.NoMore && !task.LogInStorage {
		task.LogInStorage = true
		updateTaskColumns = append(updateTaskColumns, "log_in_storage")
		remove, err = actions.TransferLogs(ctx, task.LogFilename)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("transfer logs: %w", err))
		}
	}

	if len(updateTaskColumns) != 0 {
		if err := actions_model.UpdateTask(ctx, task, updateTaskColumns...); err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("update task: %w", err))
		}
	}
	if remove != nil {
		remove()
	}

	return res, nil
}

// UpdateStepSummary stores the step summaries (GITHUB_STEP_SUMMARY markdown) of the task.
func (*Service) UpdateStepSummary(
	ctx context.Context,
	req *connect.Request[runnerv1.UpdateStepSummaryRequest],
) (*connect.Response[runnerv1.UpdateStepSummaryResponse], error) {
	runner := GetRunner(ctx)

	task, err := actions_model.GetTaskByID(ctx, req.Msg.TaskId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("get task: %w", err))
	} else if runner.ID != task.RunnerID {
		return nil, connect.NewError(connect.CodeInternal, errors.New("invalid runner for task"))
	}

	if len(req.Msg.Summaries) == 0 {
		return connect.NewResponse(&runnerv1.UpdateStepSummaryResponse{}), nil
	}

	steps, err := actions_model.GetTaskStepsByTaskID(ctx, task.ID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("get task steps: %w", err))
	}
	stepsByIndex := make(map[int64]*actions_model.ActionTaskStep, len(steps))
	for _, step := range steps {
		stepsByIndex[step.Index] = step
	}

	summaries := make([]*actions_model.ActionTaskStepSummary, 0, len(req.Msg.Summaries))
	for _, summary := range req.Msg.Summaries {
		step, ok := stepsByIndex[summary.StepNumber]
		if !ok {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("unknown step number %d for task %d", summary.StepNumber, task.ID))
		}
		summaries = append(summaries, &actions_model.ActionTaskStepSummary{
			StepID:  step.ID,
			TaskID:  task.ID,
			RepoID:  task.RepoID,
			Content: summary.Content,
		})
	}
	if err := actions_model.SaveTaskStepSummaries(ctx, summaries...); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("save step summaries: %w", err))
	}

	return connect.NewResponse(&runnerv1.UpdateStepSummaryResponse{}), nil
}

func recoverTasks(ctx context.Context, runner *actions_model.ActionRunner, requestKey string) ([]*runnerv1.Task, error) {
	// Search for previous tasks is based upon both the runner and the request key in order to reduce the security
	// risk. If a request key is leaked (eg. it appears in a log file, log file gets published in a bug report) it
	// could be used indefinitely to retrieve the associated task(s), so requiring the correctly authenticated
	// runner reduces that risk.
	recoveredTasks, err := actions_model.GetTasksByRunnerRequestKey(ctx, runner, requestKey)
	if err != nil {
		return nil, fmt.Errorf("query by request key failed: %w", err)
	} else if len(recoveredTasks) > 0 {
		// Recovered tasks from a repeat request key
		tasks, err := actions_service.RecoverTasks(ctx, recoveredTasks)
		if err != nil {
			return nil, fmt.Errorf("recover tasks failed: %w", err)
		}
		return tasks, nil
	}

	return []*runnerv1.Task{}, nil
}

func getLatestTasksVersion(ctx context.Context, ownerID, repoID int64) (int64, error) {
	latestVersion, err := actions_model.GetTasksVersionByScope(ctx, ownerID, repoID)
	if err != nil {
		return 0, fmt.Errorf("query tasks version failed: %w", err)
	} else if latestVersion == 0 {
		if err := actions_model.IncreaseTaskVersion(ctx, ownerID, repoID); err != nil {
			return 0, fmt.Errorf("fail to increase task version: %w", err)
		}
		// if we don't increase the value of `latestVersion` here,
		// the response of FetchTask will return tasksVersion as zero.
		// and the runner will treat it as an old version of Gitea.
		latestVersion++
	}

	return latestVersion, nil
}
