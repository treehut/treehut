// Copyright 2019 Gitea. All rights reserved.
// SPDX-License-Identifier: MIT

package task

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"

	admin_model "forgejo.org/models/admin"
	"forgejo.org/models/db"
	repo_model "forgejo.org/models/repo"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/graceful"
	"forgejo.org/modules/json"
	"forgejo.org/modules/keying"
	"forgejo.org/modules/log"
	base "forgejo.org/modules/migration"
	"forgejo.org/modules/queue"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/structs"
	"forgejo.org/modules/timeutil"
	"forgejo.org/modules/util"
	repo_service "forgejo.org/services/repository"
)

// taskQueue is a global queue of tasks
var taskQueue *queue.WorkerPoolQueue[*admin_model.Task]

// Run a task
func Run(ctx context.Context, t *admin_model.Task) error {
	switch t.Type {
	case structs.TaskTypeMigrateRepo:
		return runMigrateTask(ctx, t)
	default:
		return fmt.Errorf("Unknown task type: %d", t.Type)
	}
}

// Init will start the service to get all unfinished tasks and run them
func Init() error {
	taskQueue = queue.CreateSimpleQueue(graceful.GetManager().ShutdownContext(), "task", handler)
	if taskQueue == nil {
		return errors.New("unable to create task queue")
	}
	go graceful.GetManager().RunWithCancel(taskQueue)
	return nil
}

func handler(items ...*admin_model.Task) []*admin_model.Task {
	for _, task := range items {
		if err := Run(db.DefaultContext, task); err != nil {
			log.Error("Run task failed: %v", err)
		}
	}
	return nil
}

// MigrateRepository add migration repository to task
func MigrateRepository(ctx context.Context, doer, u *user_model.User, opts base.MigrateOptions) error {
	task, err := CreateMigrateTask(ctx, doer, u, opts)
	if err != nil {
		return err
	}

	return taskQueue.Push(task)
}

// CreateMigrateTask creates a migrate task
func CreateMigrateTask(ctx context.Context, doer, u *user_model.User, opts base.MigrateOptions) (*admin_model.Task, error) {
	// encrypt credentials for persistence

	task := &admin_model.Task{
		DoerID:  doer.ID,
		OwnerID: u.ID,
		Type:    structs.TaskTypeMigrateRepo,
		Status:  structs.TaskStatusQueued,
	}

	if err := db.WithTx(ctx, func(ctx context.Context) error {
		if err := admin_model.CreateTask(ctx, task); err != nil {
			return err
		}

		key := keying.MigrateTask

		opts.CloneAddrEncrypted = base64.RawStdEncoding.EncodeToString(key.Encrypt([]byte(opts.CloneAddr), keying.ColumnAndJSONSelectorAndID("payload_content", "clone_addr_encrypted", task.ID)))
		opts.CloneAddr = util.SanitizeCredentialURLs(opts.CloneAddr)

		opts.AuthPasswordEncrypted = base64.RawStdEncoding.EncodeToString(key.Encrypt([]byte(opts.AuthPassword), keying.ColumnAndJSONSelectorAndID("payload_content", "auth_password_encrypted", task.ID)))
		opts.AuthPassword = ""

		opts.AuthTokenEncrypted = base64.RawStdEncoding.EncodeToString(key.Encrypt([]byte(opts.AuthToken), keying.ColumnAndJSONSelectorAndID("payload_content", "auth_token_encrypted", task.ID)))
		opts.AuthToken = ""

		bs, err := json.Marshal(&opts)
		if err != nil {
			return err
		}
		task.PayloadContent = string(bs)

		return task.UpdateCols(ctx, "payload_content")
	}); err != nil {
		return nil, err
	}

	repo, err := repo_service.CreateRepositoryDirectly(ctx, doer, u, repo_service.CreateRepoOptions{
		Name:           opts.RepoName,
		Description:    opts.Description,
		OriginalURL:    opts.OriginalURL,
		GitServiceType: opts.GitServiceType,
		IsPrivate:      opts.Private || setting.Repository.ForcePrivate,
		IsMirror:       opts.Mirror,
		Status:         repo_model.RepositoryBeingMigrated,
	})
	if err != nil {
		task.EndTime = timeutil.TimeStampNow()
		task.Status = structs.TaskStatusFailed
		err2 := task.UpdateCols(ctx, "end_time", "status")
		if err2 != nil {
			log.Error("UpdateCols Failed: %v", err2.Error())
		}
		return nil, err
	}

	task.RepoID = repo.ID
	if err = task.UpdateCols(ctx, "repo_id"); err != nil {
		return nil, err
	}

	return task, nil
}

// RetryMigrateTask will retry the migration.
// All data, from a previous migration, is deleted before it's retried.
func RetryMigrateTask(ctx context.Context, repoID int64) error {
	migratingTask, err := admin_model.GetMigratingTask(ctx, repoID)
	if err != nil {
		return fmt.Errorf("GetMigratingTask: %w", err)
	}
	if migratingTask.Status == structs.TaskStatusQueued || migratingTask.Status == structs.TaskStatusRunning {
		return nil
	}

	// The migration is being retried, it could've failed for a variety of cases.
	// In most cases however, some data already got uploaded to the disk or
	// database. The migration code makes the assumption this is not the case and
	// if we do not clean it up, the retry attempt will fail with absolute
	// certainty.
	if err := repo_service.DeleteRepositoryDirectly(ctx, repoID, repo_service.DeleteRepositoryOpts{IgnoreOrgTeams: true, KeepMigrationBeans: true}); err != nil {
		return fmt.Errorf("DeleteRepositoryDirectly: %v", err)
	}

	// Reset task status and messages
	migratingTask.Status = structs.TaskStatusQueued
	migratingTask.Message = ""
	if err = migratingTask.UpdateCols(ctx, "status", "message"); err != nil {
		return fmt.Errorf("task.UpdateCols: %w", err)
	}

	return taskQueue.Push(migratingTask)
}

func SetMigrateTaskMessage(ctx context.Context, repoID int64, message string) error {
	migratingTask, err := admin_model.GetMigratingTask(ctx, repoID)
	if err != nil {
		log.Error("GetMigratingTask: %v", err)
		return err
	}

	migratingTask.Message = message
	if err = migratingTask.UpdateCols(ctx, "message"); err != nil {
		log.Error("task.UpdateCols failed: %v", err)
		return err
	}
	return nil
}
