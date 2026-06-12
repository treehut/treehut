// Copyright 2022 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"context"
	"crypto/subtle"
	"fmt"
	"time"

	auth_model "forgejo.org/models/auth"
	"forgejo.org/models/db"
	"forgejo.org/models/unit"
	"forgejo.org/modules/log"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/timeutil"
	"forgejo.org/modules/util"

	"code.forgejo.org/forgejo/runner/v12/act/jobparser"
	lru "github.com/hashicorp/golang-lru/v2"
	"xorm.io/builder"
)

// ActionTask represents a distribution of job
type ActionTask struct {
	ID       int64
	JobID    int64             `xorm:"index"`
	Job      *ActionRunJob     `xorm:"-"`
	Steps    []*ActionTaskStep `xorm:"-"`
	Attempt  int64
	RunnerID int64              `xorm:"index index(request_key)"`
	Status   Status             `xorm:"index"`
	Started  timeutil.TimeStamp `xorm:"index"`
	Stopped  timeutil.TimeStamp `xorm:"index(stopped_log_expired)"`

	RepoID            int64  `xorm:"index"`
	OwnerID           int64  `xorm:"index"`
	CommitSHA         string `xorm:"index"`
	IsForkPullRequest bool

	Token          string `xorm:"-"`
	TokenHash      string `xorm:"UNIQUE"` // sha256 of token
	TokenSalt      string
	TokenLastEight string `xorm:"index token_last_eight"`

	LogFilename  string     // file name of log
	LogInStorage bool       // read log from database or from storage
	LogLength    int64      // lines count
	LogSize      int64      // blob size
	LogIndexes   LogIndexes `xorm:"LONGBLOB"`                   // line number to offset
	LogExpired   bool       `xorm:"index(stopped_log_expired)"` // files that are too old will be deleted

	// When the FetchTask() API is invoked to create a task, unpreventable environmental errors may occur; for example,
	// network disconnects and timeouts. If that API call has a unique identifier associated with it, it is stored in
	// RunnerRequestKey. This allows the API call to be implemented idempotently using this state: if one API call
	// assigns a task to a runner and a second API call is received from the same runner with the same request key, the
	// existing assigned tasks can be returned.
	//
	// Indexed for an efficient search on runner_id=? AND runner_request_key=?.
	RunnerRequestKey string `xorm:"index(request_key)"`

	Created timeutil.TimeStamp `xorm:"created"`
	Updated timeutil.TimeStamp `xorm:"updated index"`
}

var successfulTokenTaskCache *lru.Cache[string, any]

func init() {
	db.RegisterModel(new(ActionTask), func() error {
		if setting.SuccessfulTokensCacheSize > 0 {
			var err error
			successfulTokenTaskCache, err = lru.New[string, any](setting.SuccessfulTokensCacheSize)
			if err != nil {
				return fmt.Errorf("unable to allocate Task cache: %v", err)
			}
		} else {
			successfulTokenTaskCache = nil
		}
		return nil
	})
}

func (task *ActionTask) Duration() time.Duration {
	return calculateDuration(task.Started, task.Stopped, task.Status)
}

func (task *ActionTask) IsStopped() bool {
	return task.Stopped > 0
}

func (task *ActionTask) GetRunLink() string {
	if task.Job == nil || task.Job.Run == nil {
		return ""
	}
	return task.Job.Run.Link()
}

func (task *ActionTask) GetCommitLink() string {
	if task.Job == nil || task.Job.Run == nil || task.Job.Run.Repo == nil {
		return ""
	}
	return task.Job.Run.Repo.CommitLink(task.CommitSHA)
}

func (task *ActionTask) GetRepoName() string {
	if task.Job == nil || task.Job.Run == nil || task.Job.Run.Repo == nil {
		return ""
	}
	return task.Job.Run.Repo.FullName()
}

func (task *ActionTask) GetRepoLink() string {
	if task.Job == nil || task.Job.Run == nil || task.Job.Run.Repo == nil {
		return ""
	}
	return task.Job.Run.Repo.Link()
}

func (task *ActionTask) LoadJob(ctx context.Context) error {
	if task.Job == nil {
		job, err := GetRunJobByID(ctx, task.JobID)
		if err != nil {
			return err
		}
		task.Job = job
	}
	return nil
}

// LoadAttributes load Job Steps if not loaded
func (task *ActionTask) LoadAttributes(ctx context.Context) error {
	if task == nil {
		return nil
	}
	if err := task.LoadJob(ctx); err != nil {
		return err
	}

	if err := task.Job.LoadAttributes(ctx); err != nil {
		return err
	}

	if task.Steps == nil { // be careful, an empty slice (not nil) also means loaded
		steps, err := GetTaskStepsByTaskID(ctx, task.ID)
		if err != nil {
			return err
		}
		task.Steps = steps
	}

	return nil
}

func (task *ActionTask) GenerateToken() {
	task.Token, task.TokenSalt, task.TokenHash, task.TokenLastEight = generateSaltedToken()
}

// After using GenerateToken, UpdateToken can be used to update the database record affecting the same columns.
func (task *ActionTask) UpdateToken(ctx context.Context) error {
	return UpdateTask(ctx, task, "token_hash", "token_salt", "token_last_eight")
}

func GetTaskByID(ctx context.Context, id int64) (*ActionTask, error) {
	var task ActionTask
	has, err := db.GetEngine(ctx).Where("id=?", id).Get(&task)
	if err != nil {
		return nil, err
	} else if !has {
		return nil, fmt.Errorf("task with id %d: %w", id, util.ErrNotExist)
	}

	return &task, nil
}

func HasTaskForRunner(ctx context.Context, runnerID int64) (bool, error) {
	return db.GetEngine(ctx).Where("runner_id = ?", runnerID).Exist(&ActionTask{})
}

func GetTaskByJobAttempt(ctx context.Context, jobID, attempt int64) (*ActionTask, error) {
	var task ActionTask
	has, err := db.GetEngine(ctx).Where("job_id=?", jobID).Where("attempt=?", attempt).Get(&task)
	if err != nil {
		return nil, err
	} else if !has {
		return nil, fmt.Errorf("task with job_id %d and attempt %d: %w", jobID, attempt, util.ErrNotExist)
	}

	return &task, nil
}

func GetRunningTaskByToken(ctx context.Context, token string) (*ActionTask, error) {
	errNotExist := fmt.Errorf("task with token %q: %w", token, util.ErrNotExist)
	if token == "" {
		return nil, errNotExist
	}
	// A token is defined as being SHA1 sum these are 40 hexadecimal bytes long
	if len(token) != 40 {
		return nil, errNotExist
	}
	for _, x := range []byte(token) {
		if x < '0' || (x > '9' && x < 'a') || x > 'f' {
			return nil, errNotExist
		}
	}

	lastEight := token[len(token)-8:]

	if id := getTaskIDFromCache(token); id > 0 {
		task := &ActionTask{
			TokenLastEight: lastEight,
		}
		// Re-get the task from the db in case it has been deleted in the intervening period
		has, err := db.GetEngine(ctx).ID(id).Get(task)
		if err != nil {
			return nil, err
		}
		if has {
			return task, nil
		}
		successfulTokenTaskCache.Remove(token)
	}

	var tasks []*ActionTask
	err := db.GetEngine(ctx).Where("token_last_eight = ? AND status = ?", lastEight, StatusRunning).Find(&tasks)
	if err != nil {
		return nil, err
	} else if len(tasks) == 0 {
		return nil, errNotExist
	}

	for _, t := range tasks {
		tempHash := auth_model.HashToken(token, t.TokenSalt)
		if subtle.ConstantTimeCompare([]byte(t.TokenHash), []byte(tempHash)) == 1 {
			if successfulTokenTaskCache != nil {
				successfulTokenTaskCache.Add(token, t.ID)
			}
			return t, nil
		}
	}
	return nil, errNotExist
}

func GetTasksByRunnerRequestKey(ctx context.Context, runner *ActionRunner, requestKey string) ([]*ActionTask, error) {
	var tasks []*ActionTask
	err := db.GetEngine(ctx).Where("runner_id = ? AND runner_request_key = ?", runner.ID, requestKey).Find(&tasks)
	if err != nil {
		return nil, err
	}
	return tasks, nil
}

func getConcurrencyCondition() builder.Cond {
	concurrencyCond := builder.NewCond()

	// OK to pick if there's no concurrency_group on the run
	concurrencyCond = concurrencyCond.Or(builder.Eq{"concurrency_group": ""})
	concurrencyCond = concurrencyCond.Or(builder.IsNull{"concurrency_group"})

	// OK to pick if it's not a "QueueBehind" concurrency type
	concurrencyCond = concurrencyCond.Or(builder.Neq{"concurrency_type": QueueBehind})

	// subQuery ends up representing all the runs that would block a run from executing:
	subQuery := builder.Select("id").From("action_run", "inner_run").
		// A run can't block itself, so exclude it from this search
		Where(builder.Neq{"inner_run.id": builder.Expr("outer_run.id")}).
		// Blocking runs must be from the same repo & concurrency group
		And(builder.Eq{"inner_run.repo_id": builder.Expr("outer_run.repo_id")}).
		And(builder.Eq{"inner_run.concurrency_group": builder.Expr("outer_run.concurrency_group")}).
		And(
			// Ideally the logic here would be that a blocking run is "not done", and "younger", which allows each run
			// to be blocked on the previous runs in the concurrency group and therefore execute in order from oldest to
			// newest.
			//
			// But it's possible for runs to be required to run out-of-order -- for example, if a younger run has
			// already completed but then it is re-run.  If we only used "not done" and "younger" as logic, then the
			// re-run would not be blocked, and therefore would violate the concurrency group's single-run goal.
			//
			// So we use two conditions to meet both needs:
			//
			// Blocking runs have a running status...
			builder.Eq{"inner_run.status": StatusRunning}.Or(
				// Blocking runs are pending execution, & are younger than the outer_run
				builder.In("inner_run.status", PendingStatuses()).
					And(builder.Lt{"inner_run.`index`": builder.Expr("outer_run.`index`")})))

	// OK to pick if there are no blocking runs
	concurrencyCond = concurrencyCond.Or(builder.NotExists(subQuery))

	return concurrencyCond
}

// Returns all the available jobs that could be executed on `runner`, before label filtering is applied.  Note that
// only a single job can actually be run from this result for any given invocation, as multiple runs (in order) from any
// single concurrency group could be returned.
func GetAvailableJobsForRunner(e db.Engine, runner *ActionRunner) ([]*ActionRunJob, error) {
	jobCond := builder.NewCond()
	if runner.RepoID != 0 {
		jobCond = builder.Eq{"repo_id": runner.RepoID}
	} else if runner.OwnerID != 0 {
		jobCond = builder.In("repo_id", builder.Select("`repository`.id").From("repository").
			Join("INNER", "repo_unit", "`repository`.id = `repo_unit`.repo_id").
			Where(builder.Eq{"`repository`.owner_id": runner.OwnerID, "`repo_unit`.type": unit.TypeActions}))
	}
	// Concurrency group checks for queuing one run behind the last run in the concurrency group are more
	// computationally expensive on the database. To manage the risk that this might have on large-scale deployments
	// When this feature is initially released, it can be disabled in the ini file by setting
	// `CONCURRENCY_GROUP_QUEUE_ENABLED = false` in the `[actions]` section.  If disabled, then actions with a
	// concurrency group and `cancel-in-progress: false` will run simultaneously rather than being queued.
	if setting.Actions.ConcurrencyGroupQueueEnabled {
		jobCond = jobCond.And(getConcurrencyCondition())
	}
	if jobCond.IsValid() {
		// It is *likely* more efficient to use an EXISTS query here rather than an IN clause, as that allows the
		// database's query optimizer to perform partial computation of the subquery rather than complete computation.
		// However, database engines can be fickle and difficult to predict. We'll retain the original IN clause
		// implementation when ConcurrencyGroupQueueEnabled is disabled, which should maintain the same performance
		// characteristics. When ConcurrencyGroupQueueEnabled is enabled, it will switch to the EXISTS clause.
		if setting.Actions.ConcurrencyGroupQueueEnabled {
			jobCond = builder.Exists(builder.Select("id").From("action_run", "outer_run").
				Where(builder.Eq{"outer_run.id": builder.Expr("action_run_job.run_id")}).
				And(jobCond))
		} else {
			jobCond = builder.In("run_id", builder.Select("id").From("action_run", "outer_run").Where(jobCond))
		}
	}

	var jobs []*ActionRunJob
	if err := e.Where("task_id=? AND status=?", 0, StatusWaiting).And(jobCond).Asc("updated", "id").Find(&jobs); err != nil {
		return nil, err
	}
	return jobs, nil
}

func CreateTaskForRunner(ctx context.Context, runner *ActionRunner, requestKey, handle *string) (*ActionTask, bool, error) {
	ctx, committer, err := db.TxContext(ctx)
	if err != nil {
		return nil, false, err
	}
	defer committer.Close()

	e := db.GetEngine(ctx)

	jobs, err := GetAvailableJobsForRunner(e, runner)
	if err != nil {
		return nil, false, err
	}

	// TODO: a more efficient way to filter labels
	var job *ActionRunJob
	log.Trace("runner labels: %v", runner.AgentLabels)
	for _, j := range jobs {
		if j.IsRequestedByRunner(handle) && j.ItRunsOn(runner.AgentLabels) {
			job = j
			break
		}
	}
	if job == nil {
		return nil, false, nil
	}
	if err := job.LoadAttributes(ctx); err != nil {
		return nil, false, err
	}

	now := timeutil.TimeStampNow()
	job.Started = now
	job.Status = StatusRunning

	task := &ActionTask{
		JobID:             job.ID,
		Attempt:           job.Attempt,
		RunnerID:          runner.ID,
		Started:           now,
		Status:            StatusRunning,
		RepoID:            job.RepoID,
		OwnerID:           job.OwnerID,
		CommitSHA:         job.CommitSHA,
		IsForkPullRequest: job.IsForkPullRequest,
	}
	if requestKey != nil {
		task.RunnerRequestKey = *requestKey
	}
	task.GenerateToken()

	var workflowJob *jobparser.Job
	if gots, err := jobparser.Parse(job.WorkflowPayload, false); err != nil {
		return nil, false, fmt.Errorf("parse workflow of job %d: %w", job.ID, err)
	} else if len(gots) != 1 {
		return nil, false, fmt.Errorf("workflow of job %d: not single workflow", job.ID)
	} else { //nolint:revive
		_, workflowJob = gots[0].Job()
	}

	if _, err := e.Insert(task); err != nil {
		return nil, false, err
	}

	task.LogFilename = logFileName(job.Run.Repo.FullName(), task.ID)
	if err := UpdateTask(ctx, task, "log_filename"); err != nil {
		return nil, false, err
	}

	if len(workflowJob.Steps) > 0 {
		steps := make([]*ActionTaskStep, len(workflowJob.Steps))
		for i, v := range workflowJob.Steps {
			name, _ := util.SplitStringAtByteN(v.String(), 255)
			steps[i] = &ActionTaskStep{
				Name:   name,
				TaskID: task.ID,
				Index:  int64(i),
				RepoID: task.RepoID,
				Status: StatusWaiting,
			}
		}
		if _, err := e.Insert(steps); err != nil {
			return nil, false, err
		}
		task.Steps = steps
	}

	job.TaskID = task.ID
	// We never have to send a notification here because the job is started with a not done status.
	if n, err := UpdateRunJobWithoutNotification(ctx, job, builder.Eq{"task_id": 0}); err != nil {
		return nil, false, err
	} else if n != 1 {
		return nil, false, nil
	}

	task.Job = job

	if err := committer.Commit(); err != nil {
		return nil, false, err
	}

	return task, true, nil
}

// Placeholder tasks are created when the status/content of an [ActionRunJob] is resolved by Forgejo without dispatch to
// a runner, specifically in the case of a workflow call's outer job.
func CreatePlaceholderTask(ctx context.Context, job *ActionRunJob, outputs map[string]string) (*ActionTask, error) {
	actionTask := &ActionTask{
		JobID:             job.ID,
		Attempt:           job.Attempt,
		Started:           timeutil.TimeStampNow(),
		Stopped:           timeutil.TimeStampNow(),
		Status:            job.Status,
		RepoID:            job.RepoID,
		OwnerID:           job.OwnerID,
		CommitSHA:         job.CommitSHA,
		IsForkPullRequest: job.IsForkPullRequest,
	}
	// token isn't used on a placeholder task, but generation is needed due to the unique constraint on field TokenHash
	actionTask.GenerateToken()

	err := db.WithTx(ctx, func(ctx context.Context) error {
		_, err := db.GetEngine(ctx).Insert(actionTask)
		if err != nil {
			return fmt.Errorf("failure inserting action_task: %w", err)
		}

		for key, value := range outputs {
			err := InsertTaskOutputIfNotExist(ctx, actionTask.ID, key, value)
			if err != nil {
				return fmt.Errorf("failure inserting action_task_output %q: %w", key, err)
			}
		}

		return nil
	})
	if err != nil {
		return nil, err
	}
	return actionTask, nil
}

func UpdateTask(ctx context.Context, task *ActionTask, cols ...string) error {
	sess := db.GetEngine(ctx).ID(task.ID)
	if len(cols) > 0 {
		sess.Cols(cols...)
	}
	_, err := sess.Update(task)
	return err
}

func FindOldTasksToExpire(ctx context.Context, olderThan timeutil.TimeStamp, limit int) ([]*ActionTask, error) {
	e := db.GetEngine(ctx)

	tasks := make([]*ActionTask, 0, limit)
	// Check "stopped > 0" to avoid deleting tasks that are still running
	return tasks, e.Where("stopped > 0 AND stopped < ? AND log_expired = ?", olderThan, false).
		Limit(limit).
		Find(&tasks)
}

func logFileName(repoFullName string, taskID int64) string {
	ret := fmt.Sprintf("%s/%02x/%d.log", repoFullName, taskID%256, taskID)

	if setting.Actions.LogCompression.IsZstd() {
		ret += ".zst"
	}

	return ret
}

func getTaskIDFromCache(token string) int64 {
	if successfulTokenTaskCache == nil {
		return 0
	}
	tInterface, ok := successfulTokenTaskCache.Get(token)
	if !ok {
		return 0
	}
	t, ok := tInterface.(int64)
	if !ok {
		return 0
	}
	return t
}
