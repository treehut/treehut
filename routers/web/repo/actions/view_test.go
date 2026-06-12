// Copyright 2025 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package actions

import (
	"fmt"
	"html/template"
	"net/http"
	"testing"

	actions_model "forgejo.org/models/actions"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unittest"
	"forgejo.org/modules/json"
	"forgejo.org/modules/translation"
	"forgejo.org/modules/web"
	"forgejo.org/services/contexttest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestActionsViewGetRunByID(t *testing.T) {
	unittest.PrepareTestEnv(t)

	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{OwnerID: 5, ID: 4})

	for _, testCase := range []struct {
		name  string
		runID int64
		err   string
	}{
		{
			name:  "Found",
			runID: 792,
		},
		{
			name:  "NotFound",
			runID: 24344,
			err:   "no such run",
		},
		{
			name:  "ZeroNotFound",
			runID: 0,
			err:   "zero is not a valid run ID",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			ctx, resp := contexttest.MockContext(t, fmt.Sprintf("user5/repo4/actions/runs/%v/artifacts/some-name", testCase.runID))
			ctx.Repo.Repository = repo
			run := getRunByID(ctx, testCase.runID)
			if testCase.err == "" {
				assert.NotNil(t, run)
				assert.False(t, ctx.Written(), resp.Body.String())
			} else {
				assert.Nil(t, run)
				assert.True(t, ctx.Written())
				assert.Contains(t, resp.Body.String(), testCase.err)
			}
		})
	}
}

func TestActionsViewArtifactsFind(t *testing.T) {
	unittest.PrepareTestEnv(t)

	for _, testCase := range []struct {
		name         string
		artifactName string
		count        int
	}{
		{
			name:         "Found",
			artifactName: "artifact-v4-download",
			count:        1,
		},
		{
			name:         "NotFound",
			artifactName: "notexist",
			count:        0,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			runID := int64(792)
			ctx, _ := contexttest.MockContext(t, fmt.Sprintf("user5/repo4/actions/runs/%v/artifacts/%v", runID, testCase.artifactName))
			artifacts := artifactsFind(ctx, actions_model.FindArtifactsOptions{
				RunID:        runID,
				ArtifactName: testCase.artifactName,
			})
			assert.False(t, ctx.Written())
			assert.Len(t, artifacts, testCase.count)
		})
	}
}

func TestActionsViewArtifactsFindByNameOrID(t *testing.T) {
	unittest.PrepareTestEnv(t)

	for _, testCase := range []struct {
		name     string
		nameOrID string
		err      string
	}{
		{
			name:     "NameFound",
			nameOrID: "artifact-v4-download",
		},
		{
			name:     "NameNotFound",
			nameOrID: "notexist",
			err:      "artifact name not found",
		},
		{
			name:     "IDFound",
			nameOrID: "22",
		},
		{
			name:     "IDNotFound",
			nameOrID: "666",
			err:      "artifact ID not found",
		},
		{
			name:     "IDZeroNotFound",
			nameOrID: "0",
			err:      "artifact name not found",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			runID := int64(792)
			ctx, resp := contexttest.MockContext(t, fmt.Sprintf("user5/repo4/actions/runs/%v/artifacts/%v", runID, testCase.nameOrID))
			artifacts := artifactsFindByNameOrID(ctx, runID, testCase.nameOrID)
			if testCase.err == "" {
				assert.NotEmpty(t, artifacts)
				assert.False(t, ctx.Written(), resp.Body.String())
			} else {
				assert.Empty(t, artifacts)
				assert.True(t, ctx.Written())
				assert.Contains(t, resp.Body.String(), testCase.err)
			}
		})
	}
}

func baseExpectedViewResponse() *ViewResponse {
	return &ViewResponse{
		State: ViewState{
			Run: ViewRunInfo{
				Link:              "/user5/repo4/actions/runs/187",
				Title:             "update actions",
				TitleHTML:         template.HTML("update actions"),
				Status:            "success",
				Description:       "actions.runs.on_push_description",
				CanCancel:         false,
				CanApprove:        false,
				CanRerun:          false,
				CanDeleteArtifact: false,
				Done:              true,
				Jobs: []*ViewJob{
					{
						ID:       192,
						Name:     "job_2",
						Status:   "success",
						CanRerun: false,
						Duration: "1m38s",
					},
				},
				Commit: ViewCommit{
					LocaleWorkflow: "actions.runs.workflow",
					LocaleAllRuns:  "actions.runs.all_runs_link",
					ShortSha:       "c2d72f5484",
					Link:           "/user5/repo4/commit/c2d72f548424103f01ee1dc02889c1e2bff816b0",
					Pusher: ViewUser{
						DisplayName: "user1",
						Link:        "/user1",
					},
					Branch: ViewBranch{
						Name:      "master",
						Link:      "/user5/repo4/src/branch/master",
						IsDeleted: false,
					},
				},
			},
			CurrentJob: ViewCurrentJob{
				Title:   "job_2",
				Details: []template.HTML{"actions.status.success"},
				Steps: []*ViewJobStep{
					{
						Summary: "Set up job",
						Status:  "running",
					},
					{
						Summary: "Complete job",
						Status:  "waiting",
					},
				},
				AllAttempts: []*TaskAttempt{
					{
						Number:            3,
						Started:           template.HTML("<relative-time prefix=\"\" tense=\"past\" datetime=\"2023-05-09T12:48:48Z\" data-tooltip-content data-tooltip-interactive=\"true\">2023-05-09 12:48:48 +00:00</relative-time>"),
						Status:            "running",
						StatusDiagnostics: []template.HTML{"actions.status.running"},
					},
					{
						Number:            2,
						Started:           template.HTML("<relative-time prefix=\"\" tense=\"past\" datetime=\"2023-05-09T12:48:48Z\" data-tooltip-content data-tooltip-interactive=\"true\">2023-05-09 12:48:48 +00:00</relative-time>"),
						Status:            "success",
						StatusDiagnostics: []template.HTML{"actions.status.success"},
					},
					{
						Number:            1,
						Started:           template.HTML("<relative-time prefix=\"\" tense=\"past\" datetime=\"2023-05-09T12:48:48Z\" data-tooltip-content data-tooltip-interactive=\"true\">2023-05-09 12:48:48 +00:00</relative-time>"),
						Status:            "success",
						StatusDiagnostics: []template.HTML{"actions.status.success"},
					},
				},
			},
		},
		Logs: ViewLogs{
			StepsLog: []*ViewStepLog{},
		},
	}
}

func TestActionsViewViewPost(t *testing.T) {
	unittest.PrepareTestEnv(t)

	tests := []struct {
		name           string
		runIndex       int64
		jobIndex       int64
		attemptNumber  int64
		expected       *ViewResponse
		expectedTweaks func(*ViewResponse)
	}{
		{
			name:          "base case",
			runIndex:      187,
			jobIndex:      0,
			attemptNumber: 1,
			expected:      baseExpectedViewResponse(),
			expectedTweaks: func(resp *ViewResponse) {
				resp.State.CurrentJob.Steps[0].Status = "success"
				resp.State.CurrentJob.Steps[1].Status = "success"
			},
		},
		{
			name:          "run with waiting jobs",
			runIndex:      189,
			jobIndex:      0,
			attemptNumber: 1,
			expected:      baseExpectedViewResponse(),
			expectedTweaks: func(resp *ViewResponse) {
				// Variations from runIndex 187 -> runIndex 189 that are not the subject of this test...
				resp.State.Run.Link = "/user5/repo4/actions/runs/189"
				resp.State.Run.Title = "job output"
				resp.State.Run.TitleHTML = "job output"
				resp.State.Run.Jobs = []*ViewJob{
					{
						ID:     194,
						Name:   "job1 (1)",
						Status: "success",
					},
					{
						ID:     195,
						Name:   "job1 (2)",
						Status: "success",
					},
					{
						ID:     196,
						Name:   "job2",
						Status: "waiting",
					},
				}
				resp.State.CurrentJob.Title = "job1 (1)"
				resp.State.CurrentJob.Steps = []*ViewJobStep{
					{
						Summary: "Set up job",
						Status:  "success",
					},
					{
						Summary: "Complete job",
						Status:  "success",
					},
				}
				resp.State.CurrentJob.AllAttempts = []*TaskAttempt{
					{
						Number:            1,
						Started:           template.HTML("<relative-time prefix=\"\" tense=\"past\" datetime=\"2023-05-09T12:48:48Z\" data-tooltip-content data-tooltip-interactive=\"true\">2023-05-09 12:48:48 +00:00</relative-time>"),
						Status:            "success",
						StatusDiagnostics: []template.HTML{"actions.status.success"},
					},
				}

				// Under test in this case: verify that Done is set to false; in the fixture data, job.ID=195 is status
				// Success, but job.ID=196 is status Waiting, and so we expect to signal Done=false to indicate to the
				// UI to continue refreshing the page.
				resp.State.Run.Done = false
			},
		},
		{
			name:          "attempt 3",
			runIndex:      187,
			jobIndex:      0,
			attemptNumber: 3,
			expected:      baseExpectedViewResponse(),
			expectedTweaks: func(resp *ViewResponse) {
				resp.State.CurrentJob.Steps[0].Status = "running"
				resp.State.CurrentJob.Steps[1].Status = "waiting"
			},
		},
		{
			// This ActionRunJob has TaskID: null, which allows us to access out-of-range attempts without errors and
			// with just some stub data for the UI to start waiting around on.
			name:          "attempt out-of-bounds on non-picked task",
			runIndex:      190,
			jobIndex:      0,
			attemptNumber: 100,
			expected:      baseExpectedViewResponse(),
			expectedTweaks: func(resp *ViewResponse) {
				// Variations from runIndex 187 -> runIndex 190 that are not the subject of this test...
				resp.State.Run.Link = "/user5/repo4/actions/runs/190"
				resp.State.Run.Title = "job output"
				resp.State.Run.TitleHTML = "job output"
				resp.State.Run.Done = false
				resp.State.Run.Jobs = []*ViewJob{
					{
						ID:     396,
						Name:   "job_2",
						Status: "waiting",
					},
				}
				resp.State.Run.Commit.Branch = ViewBranch{
					Name:      "test",
					Link:      "/user5/repo4/src/branch/test",
					IsDeleted: true,
				}

				// Expected blank data in the response because this job isn't picked by a runner yet.  Keep details here
				// in-sync with the RepoActionView 'view non-picked action run job' test.
				resp.State.CurrentJob.Details = []template.HTML{"actions.status.diagnostics.waiting"}
				resp.State.CurrentJob.Steps = []*ViewJobStep{}
				resp.State.CurrentJob.AllAttempts = []*TaskAttempt{
					{
						Number:            1,
						Started:           template.HTML("actions.jobs.not_started"),
						Status:            "waiting",
						StatusDiagnostics: []template.HTML{"actions.status.diagnostics.waiting"},
					},
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, resp := contexttest.MockContext(t, "user2/repo1/actions/runs/0")
			contexttest.LoadUser(t, ctx, 2)
			contexttest.LoadRepo(t, ctx, 4)
			ctx.SetParams(":run", fmt.Sprintf("%d", tt.runIndex))
			ctx.SetParams(":job", fmt.Sprintf("%d", tt.jobIndex))
			ctx.SetParams(":attempt", fmt.Sprintf("%d", tt.attemptNumber))
			web.SetForm(ctx, &ViewRequest{})

			ViewPost(ctx)
			require.Equal(t, http.StatusOK, resp.Result().StatusCode, "failure in ViewPost(): %q", resp.Body.String())

			var actual ViewResponse
			err := json.Unmarshal(resp.Body.Bytes(), &actual)
			require.NoError(t, err)

			// `Duration` field is dynamic based upon current time, so eliminate it from comparison -- but check that it
			// has the right format at least.
			zeroDurations := func(vr *ViewResponse) {
				for _, job := range vr.State.Run.Jobs {
					assert.Regexp(t, `^(\d+[hms]){1,3}$`, job.Duration)
					job.Duration = ""
				}
				for _, step := range vr.State.CurrentJob.Steps {
					step.Duration = ""
				}
			}
			zeroDurations(&actual)
			zeroDurations(tt.expected)
			tt.expectedTweaks(tt.expected)

			assert.Equal(t, *tt.expected, actual)
		})
	}
}

func TestActionsViewCancelableUntilAllJobsFinished(t *testing.T) {
	unittest.PrepareTestEnv(t)

	tests := []struct {
		name     string
		runIndex int64
		assert   func(*testing.T, *ViewResponse)
	}{
		{
			name:     "failed and running",
			runIndex: 191,
			assert: func(t *testing.T, actual *ViewResponse) {
				assert.Equal(t, "failure", actual.State.Run.Jobs[0].Status)
				assert.Equal(t, "running", actual.State.Run.Jobs[1].Status)
				assert.True(t, actual.State.Run.CanCancel)
			},
		},
		{
			name:     "failed and success",
			runIndex: 192,
			assert: func(t *testing.T, actual *ViewResponse) {
				assert.Equal(t, "failure", actual.State.Run.Jobs[0].Status)
				assert.Equal(t, "success", actual.State.Run.Jobs[1].Status)
				assert.False(t, actual.State.Run.CanCancel)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, resp := contexttest.MockContext(t, "user2/repo1/actions/runs/0")
			contexttest.LoadUser(t, ctx, 1)
			contexttest.LoadRepo(t, ctx, 4)
			ctx.SetParams(":run", fmt.Sprintf("%d", tt.runIndex))
			ctx.SetParams(":attempt", fmt.Sprintf("%d", 0))
			web.SetForm(ctx, &ViewRequest{})

			ViewPost(ctx)
			require.Equal(t, http.StatusOK, resp.Result().StatusCode, "failure in ViewPost(): %q", resp.Body.String())

			var actual ViewResponse
			err := json.Unmarshal(resp.Body.Bytes(), &actual)
			require.NoError(t, err)

			tt.assert(t, &actual)
		})
	}
}

func TestActionsViewRedirectToLatestAttempt(t *testing.T) {
	unittest.PrepareTestEnv(t)

	tests := []struct {
		name         string
		runIndex     int64
		jobIndex     int64
		expectedCode int
		expectedURL  string
		userID       int64
		repoID       int64
	}{
		{
			name:        "no job index",
			runIndex:    187,
			jobIndex:    -1,
			expectedURL: "https://try.gitea.io/user2/repo1/actions/runs/187/jobs/0/attempt/1",
		},
		{
			name:        "job w/ 1 attempt",
			runIndex:    187,
			jobIndex:    0,
			expectedURL: "https://try.gitea.io/user2/repo1/actions/runs/187/jobs/0/attempt/1",
		},
		{
			name:        "job w/ multiple attempts",
			runIndex:    187,
			jobIndex:    2,
			expectedURL: "https://try.gitea.io/user2/repo1/actions/runs/187/jobs/2/attempt/2",
		},
		{
			name:         "run out-of-range",
			runIndex:     5000,
			jobIndex:     -1,
			expectedCode: http.StatusNotFound,
		},
		// Odd behavior with an out-of-bound jobIndex -- defaults to the first job.  This is existing behavior
		// documented in the getRunJobs internal helper which... seems not perfect for the redirect... but it's high
		// risk to change and it's an OK user outcome to be redirected to something valid in the requested run.
		{
			name:        "job out-of-range",
			runIndex:    187,
			jobIndex:    500,
			expectedURL: "https://try.gitea.io/user2/repo1/actions/runs/187/jobs/0/attempt/1",
		},
		// This ActionRunJob has Attempt: 0 and TaskID: null, which indicates its first run is pending pickup by a
		// runner.  Should redirect to the attempt/1 since that's what it will be when it is running.
		{
			name:        "redirect to non-picked task",
			userID:      2,
			repoID:      4,
			runIndex:    190,
			jobIndex:    0,
			expectedURL: "https://try.gitea.io/user5/repo4/actions/runs/190/jobs/0/attempt/1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, resp := contexttest.MockContext(t, "user2/repo1/actions/runs/0")
			if tt.userID == 0 {
				contexttest.LoadUser(t, ctx, 2)
			} else {
				contexttest.LoadUser(t, ctx, tt.userID)
			}
			if tt.repoID == 0 {
				contexttest.LoadRepo(t, ctx, 1)
			} else {
				contexttest.LoadRepo(t, ctx, tt.repoID)
			}
			ctx.SetParams(":run", fmt.Sprintf("%d", tt.runIndex))
			if tt.jobIndex != -1 {
				ctx.SetParams(":job", fmt.Sprintf("%d", tt.jobIndex))
			}

			RedirectToLatestAttempt(ctx)
			if tt.expectedCode == 0 {
				assert.Equal(t, http.StatusTemporaryRedirect, resp.Code)
				url, err := resp.Result().Location()
				require.NoError(t, err)
				assert.Equal(t, tt.expectedURL, url.String())
			} else {
				assert.Equal(t, tt.expectedCode, resp.Code)
			}
		})
	}
}

func TestActionsRerun(t *testing.T) {
	tests := []struct {
		name         string
		runIndex     int64
		jobIndex     int64
		expectedCode int
		expectedURL  string
		expectedBody string
	}{
		{
			name:         "rerun all",
			runIndex:     138574,
			jobIndex:     -1,
			expectedCode: 200,
			expectedURL:  "https://try.gitea.io/user2/repo1/actions/runs/138574/jobs/0/attempt/3",
		},
		{
			name:         "rerun job",
			runIndex:     138574,
			jobIndex:     2,
			expectedCode: 200,
			expectedURL:  "https://try.gitea.io/user2/repo1/actions/runs/138574/jobs/2/attempt/6",
		},
		{
			name:         "rerun workflow that cannot be run",
			runIndex:     138575,
			jobIndex:     -1,
			expectedCode: 400,
			expectedBody: "{\"errorMessage\":\"actions.workflow.rerun_impossible\",\"renderFormat\":\"html\"}\n",
		},
		{
			name:         "rerun job that cannot be run",
			runIndex:     138575,
			jobIndex:     1,
			expectedCode: 400,
			expectedBody: "{\"errorMessage\":\"actions.workflow.rerun_impossible\",\"renderFormat\":\"html\"}\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer unittest.OverrideFixtures("routers/web/repo/actions/TestActionsRerun")()
			unittest.PrepareTestEnv(t)

			ctx, resp := contexttest.MockContext(t, "user2/repo1/actions/runs/138574/rerun")
			contexttest.LoadUser(t, ctx, 2)
			contexttest.LoadRepo(t, ctx, 1)
			ctx.SetParams(":run", fmt.Sprintf("%d", tt.runIndex))
			if tt.jobIndex != -1 {
				ctx.SetParams(":job", fmt.Sprintf("%d", tt.jobIndex))
			}

			Rerun(ctx)

			if tt.expectedCode < 300 {
				require.Equal(t, tt.expectedCode, resp.Result().StatusCode, "failure in Rerun(): %q", resp.Body.String())

				var actual redirectObject
				err := json.Unmarshal(resp.Body.Bytes(), &actual)
				require.NoError(t, err)

				// Note: this test isn't doing any functional testing of the Rerun handler's actual ability to set up a job
				// rerun.  This test was added when the redirect to the correct `attempt` was added and only covers that
				// addition at this time.
				assert.Equal(t, redirectObject{Redirect: tt.expectedURL}, actual)
			} else {
				require.Equal(t, tt.expectedCode, resp.Result().StatusCode)
				assert.Equal(t, tt.expectedBody, resp.Body.String())
			}
		})
	}
}

func TestActionsViewStatusDiagnostics(t *testing.T) {
	translation.InitLocales(t.Context())
	english := translation.NewLocale("en-US")

	testCases := []struct {
		name     string
		status   actions_model.Status
		job      actions_model.ActionRunJob
		expected []template.HTML
	}{
		{
			name:     "Unknown status",
			status:   actions_model.StatusUnknown,
			job:      actions_model.ActionRunJob{RunsOn: []string{"windows"}, Run: &actions_model.ActionRun{NeedApproval: false}},
			expected: []template.HTML{"Unknown"},
		},
		{
			name:     "Waiting without labels",
			status:   actions_model.StatusWaiting,
			job:      actions_model.ActionRunJob{RunsOn: []string{}, Run: &actions_model.ActionRun{NeedApproval: false}},
			expected: []template.HTML{"Waiting for a runner with the following labels: "},
		},
		{
			name:     "Waiting with one label",
			status:   actions_model.StatusWaiting,
			job:      actions_model.ActionRunJob{RunsOn: []string{"freebsd"}, Run: &actions_model.ActionRun{NeedApproval: false}},
			expected: []template.HTML{"Waiting for a runner with the following label: freebsd"},
		},
		{
			name:     "Waiting with labels, no approval",
			status:   actions_model.StatusWaiting,
			job:      actions_model.ActionRunJob{RunsOn: []string{"docker", "ubuntu"}, Run: &actions_model.ActionRun{NeedApproval: false}},
			expected: []template.HTML{"Waiting for a runner with the following labels: docker, ubuntu"},
		},
		{
			name:   "Waiting with labels, approval",
			status: actions_model.StatusWaiting,
			job:    actions_model.ActionRunJob{RunsOn: []string{"docker", "ubuntu"}, Run: &actions_model.ActionRun{NeedApproval: true}},
			expected: []template.HTML{
				"Waiting for a runner with the following labels: docker, ubuntu",
				"Need approval to run workflows for fork pull request.",
			},
		},
		{
			name:     "Running",
			status:   actions_model.StatusRunning,
			job:      actions_model.ActionRunJob{RunsOn: []string{"debian"}, Run: &actions_model.ActionRun{NeedApproval: false}},
			expected: []template.HTML{"Running"},
		},
		{
			name:     "Success",
			status:   actions_model.StatusSuccess,
			job:      actions_model.ActionRunJob{RunsOn: []string{"debian"}, Run: &actions_model.ActionRun{NeedApproval: false}},
			expected: []template.HTML{"Success"},
		},
		{
			name:     "Failure",
			status:   actions_model.StatusFailure,
			job:      actions_model.ActionRunJob{RunsOn: []string{"debian"}, Run: &actions_model.ActionRun{NeedApproval: false}},
			expected: []template.HTML{"Failure"},
		},
		{
			name:     "Cancelled",
			status:   actions_model.StatusCancelled,
			job:      actions_model.ActionRunJob{RunsOn: []string{"debian"}, Run: &actions_model.ActionRun{NeedApproval: false}},
			expected: []template.HTML{"Canceled"},
		},
		{
			name:     "Skipped",
			status:   actions_model.StatusSkipped,
			job:      actions_model.ActionRunJob{RunsOn: []string{"debian"}, Run: &actions_model.ActionRun{NeedApproval: false}},
			expected: []template.HTML{"Skipped"},
		},
		{
			name:     "Blocked",
			status:   actions_model.StatusBlocked,
			job:      actions_model.ActionRunJob{RunsOn: []string{"debian"}, Run: &actions_model.ActionRun{NeedApproval: false}},
			expected: []template.HTML{"Blocked"},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.expected, statusDiagnostics(testCase.status, &testCase.job, english))
		})
	}
}
