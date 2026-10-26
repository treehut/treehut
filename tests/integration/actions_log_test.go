// Copyright 2024 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	auth_model "forgejo.org/models/auth"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/storage"
	"forgejo.org/modules/test"

	runnerv1 "code.forgejo.org/forgejo/actions-proto/runner/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestActionsDownloadTaskLogs(t *testing.T) {
	if !setting.Database.Type.IsSQLite3() {
		t.Skip()
	}
	now := time.Now()
	testCases := []struct {
		treePath    string
		fileContent string
		outcome     *mockTaskOutcome
		zstdEnabled bool
	}{
		{
			treePath: ".gitea/workflows/download-task-logs-zstd.yml",
			fileContent: `name: download-task-logs-zstd
on:
  push:
    paths:
      - '.gitea/workflows/download-task-logs-zstd.yml'
jobs:
    job1:
      runs-on: ubuntu-latest
      steps:
        - run: echo job1 with zstd enabled
`,
			outcome: &mockTaskOutcome{
				result: runnerv1.Result_RESULT_SUCCESS,
				logRows: []*runnerv1.LogRow{
					{
						Time:    timestamppb.New(now.Add(1 * time.Second)),
						Content: "  \U0001F433  docker create image",
					},
					{
						Time:    timestamppb.New(now.Add(2 * time.Second)),
						Content: "job1 zstd enabled",
					},
					{
						Time:    timestamppb.New(now.Add(3 * time.Second)),
						Content: "\U0001F3C1  Job succeeded",
					},
				},
			},
			zstdEnabled: true,
		},
		{
			treePath: ".gitea/workflows/download-task-logs-no-zstd.yml",
			fileContent: `name: download-task-logs-no-zstd
on:
  push:
    paths:
      - '.gitea/workflows/download-task-logs-no-zstd.yml'
jobs:
    job1:
      runs-on: ubuntu-latest
      steps:
        - run: echo job1 with zstd disabled
`,
			outcome: &mockTaskOutcome{
				result: runnerv1.Result_RESULT_SUCCESS,
				logRows: []*runnerv1.LogRow{
					{
						Time:    timestamppb.New(now.Add(4 * time.Second)),
						Content: "  \U0001F433  docker create image",
					},
					{
						Time:    timestamppb.New(now.Add(5 * time.Second)),
						Content: "job1 zstd disabled",
					},
					{
						Time:    timestamppb.New(now.Add(6 * time.Second)),
						Content: "\U0001F3C1  Job succeeded",
					},
				},
			},
			zstdEnabled: false,
		},
	}
	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
		session := loginUser(t, user2.Name)
		token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteRepository, auth_model.AccessTokenScopeWriteUser)

		apiRepo := createActionsTestRepo(t, token, "actions-download-task-logs", false)
		repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: apiRepo.ID})
		runner := newMockRunner()
		runner.registerAsRepoRunner(t, user2.Name, repo.Name, "mock-runner", []string{"ubuntu-latest"})

		for _, tc := range testCases {
			t.Run(fmt.Sprintf("test %s", tc.treePath), func(t *testing.T) {
				var resetFunc func()
				if tc.zstdEnabled {
					resetFunc = test.MockVariableValue(&setting.Actions.LogCompression, "zstd")
					assert.True(t, setting.Actions.LogCompression.IsZstd())
				} else {
					resetFunc = test.MockVariableValue(&setting.Actions.LogCompression, "none")
					assert.False(t, setting.Actions.LogCompression.IsZstd())
				}

				// create the workflow file
				opts := getWorkflowCreateFileOptions(user2, repo.DefaultBranch, fmt.Sprintf("create %s", tc.treePath), tc.fileContent)
				createWorkflowFile(t, token, user2.Name, repo.Name, tc.treePath, opts)

				// fetch and execute task
				task := runner.fetchTask(t)
				runner.execTask(t, task, tc.outcome)

				// check whether the log file exists
				logFileName := fmt.Sprintf("%s/%02x/%d.log", repo.FullName(), task.Id%256, task.Id)
				if setting.Actions.LogCompression.IsZstd() {
					logFileName += ".zst"
				}
				_, err := storage.Actions.Stat(logFileName)
				require.NoError(t, err)

				// download task logs and check content
				runIndex := task.Context.GetFields()["run_number"].GetStringValue()
				attempt := task.Context.GetFields()["run_attempt"].GetStringValue()
				logURL := fmt.Sprintf("/%s/%s/actions/runs/%s/jobs/0/attempt/%s/logs", user2.Name, repo.Name, runIndex, attempt)
				req := NewRequest(t, "GET", logURL)
				req.AddTokenAuth(token)
				resp := MakeRequest(t, req, http.StatusOK)
				logTextLines := strings.Split(strings.TrimSpace(resp.Body.String()), "\n")
				assert.Len(t, logTextLines, len(tc.outcome.logRows))
				for idx, lr := range tc.outcome.logRows {
					assert.Equal(
						t,
						fmt.Sprintf("%s %s", lr.Time.AsTime().Format("2006-01-02T15:04:05.0000000Z07:00"), lr.Content),
						logTextLines[idx],
					)
				}

				resetFunc()
			})
		}

		httpContext := NewAPITestContext(t, user2.Name, repo.Name, auth_model.AccessTokenScopeWriteUser)
		doAPIDeleteRepository(httpContext)(t)
	})
}

func TestActionsDownloadTaskRerunLogs(t *testing.T) {
	if !setting.Database.Type.IsSQLite3() {
		t.Skip()
	}
	now := time.Now()
	treePath := ".gitea/workflows/download-task-logs.yml"
	fileContent := `name: download-task-logs
on:
  push:
    paths:
      - '.gitea/workflows/download-task-logs.yml'
jobs:
    job1:
      runs-on: ubuntu-latest
      steps:
        - run: |
            echo "Run attempt: ${{ github.run_attempt }}"
`
	firstOutcome := &mockTaskOutcome{
		result: runnerv1.Result_RESULT_SUCCESS,
		logRows: []*runnerv1.LogRow{
			{
				Time:    timestamppb.New(now.Add(1 * time.Second)),
				Content: "  \U0001F433  docker create image",
			},
			{
				Time:    timestamppb.New(now.Add(2 * time.Second)),
				Content: "Run attempt: 1",
			},
			{
				Time:    timestamppb.New(now.Add(3 * time.Second)),
				Content: "\U0001F3C1  Job succeeded",
			},
		},
	}
	secondOutcome := &mockTaskOutcome{
		result: runnerv1.Result_RESULT_SUCCESS,
		logRows: []*runnerv1.LogRow{
			{
				Time:    timestamppb.New(now.Add(1 * time.Second)),
				Content: "  \U0001F433  docker create image",
			},
			{
				Time:    timestamppb.New(now.Add(2 * time.Second)),
				Content: "Run attempt: 2",
			},
			{
				Time:    timestamppb.New(now.Add(3 * time.Second)),
				Content: "\U0001F3C1  Job succeeded",
			},
		},
	}

	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
		session := loginUser(t, user2.Name)
		token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteRepository, auth_model.AccessTokenScopeWriteUser)

		repo := createActionsTestRepo(t, token, "actions-download-task-logs", false)

		runner := newMockRunner()
		runner.registerAsRepoRunner(t, user2.Name, repo.Name, "mock-runner", []string{"ubuntu-latest"})

		opts := getWorkflowCreateFileOptions(user2, repo.DefaultBranch, fmt.Sprintf("Create %s", treePath), fileContent)
		createWorkflowFile(t, token, user2.Name, repo.Name, treePath, opts)

		// Execute first run
		task := runner.fetchTask(t)
		runner.execTask(t, task, firstOutcome)

		// Download task logs and check content
		runIndex := task.Context.GetFields()["run_number"].GetStringValue()
		attempt := task.Context.GetFields()["run_attempt"].GetStringValue()
		logURL := fmt.Sprintf("/%s/%s/actions/runs/%s/jobs/0/attempt/%s/logs", user2.Name, repo.Name, runIndex, attempt)
		logRequest := NewRequest(t, "GET", logURL)
		logResponse := session.MakeRequest(t, logRequest, http.StatusOK)
		logTextLines := strings.Split(strings.TrimSpace(logResponse.Body.String()), "\n")
		assert.Len(t, logTextLines, len(firstOutcome.logRows))
		for idx, lr := range firstOutcome.logRows {
			assert.Equal(
				t,
				fmt.Sprintf("%s %s", lr.Time.AsTime().Format("2006-01-02T15:04:05.0000000Z07:00"), lr.Content),
				logTextLines[idx],
			)
		}

		// Trigger rerun
		rerunURL := fmt.Sprintf("/%s/%s/actions/runs/%s/rerun", user2.Name, repo.Name, runIndex)
		rerunRequest := NewRequest(t, "POST", rerunURL)
		session.MakeRequest(t, rerunRequest, http.StatusOK)

		// Execute rerun task
		rerunTask := runner.fetchTask(t)
		runner.execTask(t, rerunTask, secondOutcome)

		// Download rerun task logs and check content
		rerunIndex := rerunTask.Context.GetFields()["run_number"].GetStringValue()
		rerunAttempt := rerunTask.Context.GetFields()["run_attempt"].GetStringValue()
		rerunLogURL := fmt.Sprintf("/%s/%s/actions/runs/%s/jobs/0/attempt/%s/logs", user2.Name, repo.Name, rerunIndex, rerunAttempt)
		rerunLogRequest := NewRequest(t, "GET", rerunLogURL)
		rerunLogResponse := session.MakeRequest(t, rerunLogRequest, http.StatusOK)
		rerunLogTextLines := strings.Split(strings.TrimSpace(rerunLogResponse.Body.String()), "\n")
		assert.Len(t, rerunLogTextLines, len(secondOutcome.logRows))
		for idx, lr := range secondOutcome.logRows {
			assert.Equal(
				t,
				fmt.Sprintf("%s %s", lr.Time.AsTime().Format("2006-01-02T15:04:05.0000000Z07:00"), lr.Content),
				rerunLogTextLines[idx],
			)
		}

		httpContext := NewAPITestContext(t, user2.Name, repo.Name, auth_model.AccessTokenScopeWriteUser)
		doAPIDeleteRepository(httpContext)(t)
	})
}

func TestActionsFinishLogsWithEmptyRowsArray(t *testing.T) {
	if !setting.Database.Type.IsSQLite3() {
		t.Skip()
	}

	now := time.Now()

	treePath := ".gitea/workflows/download-task-logs-zstd.yml"
	fileContent := `name: download-task-logs-zstd
on:
  push:
    paths:
      - '.gitea/workflows/download-task-logs-zstd.yml'
jobs:
    job1:
      runs-on: ubuntu-latest
      steps:
        - run: echo job1 with zstd enabled
`
	outcome := &mockTaskOutcome{
		result: runnerv1.Result_RESULT_SUCCESS,
		logRows: []*runnerv1.LogRow{
			{
				Time:    timestamppb.New(now.Add(1 * time.Second)),
				Content: "  \U0001F433  docker create image",
			},
			{
				Time:    timestamppb.New(now.Add(2 * time.Second)),
				Content: "job1 zstd enabled",
			},
			{
				Time:    timestamppb.New(now.Add(3 * time.Second)),
				Content: "\U0001F3C1  Job succeeded",
			},
		},
		// Triggers the "finish logs with empty rows" behaviour that this test case is intended for:
		sendEmptyUpdateLog: true,
	}

	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
		session := loginUser(t, user2.Name)
		token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteRepository, auth_model.AccessTokenScopeWriteUser)

		apiRepo := createActionsTestRepo(t, token, "actions-finish-logs-with-empty-rows-array", false)
		repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: apiRepo.ID})
		runner := newMockRunner()
		runner.registerAsRepoRunner(t, user2.Name, repo.Name, "mock-runner", []string{"ubuntu-latest"})

		// create the workflow file
		opts := getWorkflowCreateFileOptions(user2, repo.DefaultBranch, fmt.Sprintf("create %s", treePath), fileContent)
		createWorkflowFile(t, token, user2.Name, repo.Name, treePath, opts)

		// fetch and execute task
		task := runner.fetchTask(t)
		runner.execTask(t, task, outcome)

		// check whether the log file exists
		logFileName := fmt.Sprintf("%s/%02x/%d.log", repo.FullName(), task.Id%256, task.Id)
		if setting.Actions.LogCompression.IsZstd() {
			logFileName += ".zst"
		}
		_, err := storage.Actions.Stat(logFileName)
		require.NoError(t, err)

		// download task logs and check content
		runIndex := task.Context.GetFields()["run_number"].GetStringValue()
		attempt := task.Context.GetFields()["run_attempt"].GetStringValue()
		logURL := fmt.Sprintf("/%s/%s/actions/runs/%s/jobs/0/attempt/%s/logs", user2.Name, repo.Name, runIndex, attempt)
		req := NewRequest(t, "GET", logURL)
		req.AddTokenAuth(token)
		resp := MakeRequest(t, req, http.StatusOK)
		logTextLines := strings.Split(strings.TrimSpace(resp.Body.String()), "\n")
		assert.Len(t, logTextLines, len(outcome.logRows))
		for idx, lr := range outcome.logRows {
			assert.Equal(
				t,
				fmt.Sprintf("%s %s", lr.Time.AsTime().Format("2006-01-02T15:04:05.0000000Z07:00"), lr.Content),
				logTextLines[idx],
			)
		}

		httpContext := NewAPITestContext(t, user2.Name, repo.Name, auth_model.AccessTokenScopeWriteUser)
		doAPIDeleteRepository(httpContext)(t)
	})
}
