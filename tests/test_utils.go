// Copyright 2017 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

//nolint:forbidigo
package tests

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"forgejo.org/models/db"
	packages_model "forgejo.org/models/packages"
	repo_model "forgejo.org/models/repo"
	unit_model "forgejo.org/models/unit"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/base"
	"forgejo.org/modules/git"
	"forgejo.org/modules/graceful"
	"forgejo.org/modules/log"
	"forgejo.org/modules/options"
	"forgejo.org/modules/process"
	repo_module "forgejo.org/modules/repository"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/storage"
	"forgejo.org/modules/testlogger"
	"forgejo.org/modules/util"
	"forgejo.org/routers"
	"forgejo.org/services/notify"
	files_service "forgejo.org/services/repository/files"
	"forgejo.org/tests/forgery"

	"github.com/stretchr/testify/require"
	"xorm.io/xorm/convert"

	_ "github.com/jackc/pgx/v5/stdlib" // Import pgx driver
)

func exitf(format string, args ...any) {
	fmt.Printf(format+"\n", args...)
	os.Exit(1)
}

var preparedDir string

func InitTest(requireGitea bool) {
	log.RegisterEventWriter("test", testlogger.NewTestLoggerWriter)

	giteaRoot := base.SetupGiteaRoot()
	if giteaRoot == "" {
		exitf("Environment variable $GITEA_ROOT not set")
	}

	// TODO: Speedup tests that rely on the event source ticker, confirm whether there is any bug or failure.
	// setting.UI.Notification.EventSourceUpdateTime = time.Second

	setting.IsInTesting = true
	setting.AppWorkPath = giteaRoot
	setting.CustomPath = filepath.Join(setting.AppWorkPath, "custom")
	if requireGitea {
		giteaBinary := "gitea"
		setting.AppPath = path.Join(giteaRoot, giteaBinary)
		if _, err := os.Stat(setting.AppPath); err != nil {
			exitf("Could not find gitea binary at %s", setting.AppPath)
		}
	}
	giteaConf := os.Getenv("GITEA_CONF")
	if giteaConf == "" {
		// By default, use sqlite.ini for testing, then IDE like GoLand can start the test process with debugger.
		// It's easier for developers to debug bugs step by step with a debugger.
		// Notice: when doing "ssh push", Gitea executes sub processes, debugger won't work for the sub processes.
		giteaConf = "tests/sqlite.ini"
		_ = os.Setenv("GITEA_CONF", giteaConf)
		fmt.Printf("Environment variable $GITEA_CONF not set, use default: %s\n", giteaConf)
		if !setting.EnableSQLite3 {
			exitf(`sqlite3 requires: import _ "github.com/mattn/go-sqlite3" or -tags sqlite,sqlite_unlock_notify`)
		}
	}
	if !path.IsAbs(giteaConf) {
		setting.CustomConf = filepath.Join(giteaRoot, giteaConf)
	} else {
		setting.CustomConf = giteaConf
	}

	unittest.InitSettings()
	setting.Repository.DefaultBranch = "master" // many test code still assume that default branch is called "master"
	_ = util.RemoveAll(repo_module.LocalCopyPath())

	if err := git.InitFull(context.Background()); err != nil {
		log.Fatal("git.InitOnceWithSync: %v", err)
	}

	setting.LoadDBSetting()
	if err := storage.Init(); err != nil {
		exitf("Init storage failed: %v", err)
	}

	switch {
	case setting.Database.Type.IsMySQL():
		connType := "tcp"
		if len(setting.Database.Host) > 0 && setting.Database.Host[0] == '/' { // looks like a unix socket
			connType = "unix"
		}

		db, err := sql.Open("mysql", fmt.Sprintf("%s:%s@%s(%s)/",
			setting.Database.User, setting.Database.Passwd, connType, setting.Database.Host))
		defer db.Close()
		if err != nil {
			log.Fatal("sql.Open: %v", err)
		}
		if _, err = db.Exec(fmt.Sprintf("CREATE DATABASE IF NOT EXISTS %s", strings.SplitN(setting.Database.Name, "?", 2)[0])); err != nil {
			log.Fatal("db.Exec: %v", err)
		}
	case setting.Database.Type.IsPostgreSQL():
		var db *sql.DB
		var err error
		if setting.Database.Host[0] == '/' {
			db, err = sql.Open("pgx", fmt.Sprintf("postgres://%s:%s@/%s?sslmode=%s&host=%s",
				setting.Database.User, setting.Database.Passwd, setting.Database.Name, setting.Database.SSLMode, setting.Database.Host))
		} else {
			db, err = sql.Open("pgx", fmt.Sprintf("postgres://%s:%s@%s/%s?sslmode=%s",
				setting.Database.User, setting.Database.Passwd, setting.Database.Host, setting.Database.Name, setting.Database.SSLMode))
		}

		defer db.Close()
		if err != nil {
			log.Fatal("sql.Open: %v", err)
		}
		dbrows, err := db.Query(fmt.Sprintf("SELECT 1 FROM pg_database WHERE datname = '%s'", setting.Database.Name))
		if err != nil {
			log.Fatal("db.Query: %v", err)
		}
		defer dbrows.Close()

		if !dbrows.Next() {
			if _, err = db.Exec(fmt.Sprintf("CREATE DATABASE %s", setting.Database.Name)); err != nil {
				log.Fatal("db.Exec: CREATE DATABASE: %v", err)
			}
		}
		// Check if we need to setup a specific schema
		if len(setting.Database.Schema) == 0 {
			break
		}
		db.Close()

		if setting.Database.Host[0] == '/' {
			db, err = sql.Open("pgx", fmt.Sprintf("postgres://%s:%s@/%s?sslmode=%s&host=%s",
				setting.Database.User, setting.Database.Passwd, setting.Database.Name, setting.Database.SSLMode, setting.Database.Host))
		} else {
			db, err = sql.Open("pgx", fmt.Sprintf("postgres://%s:%s@%s/%s?sslmode=%s",
				setting.Database.User, setting.Database.Passwd, setting.Database.Host, setting.Database.Name, setting.Database.SSLMode))
		}
		// This is a different db object; requires a different Close()
		defer db.Close()
		if err != nil {
			log.Fatal("sql.Open: %v", err)
		}
		schrows, err := db.Query(fmt.Sprintf("SELECT 1 FROM information_schema.schemata WHERE schema_name = '%s'", setting.Database.Schema))
		if err != nil {
			log.Fatal("db.Query: %v", err)
		}
		defer schrows.Close()

		if !schrows.Next() {
			// Create and setup a DB schema
			if _, err = db.Exec(fmt.Sprintf("CREATE SCHEMA %s", setting.Database.Schema)); err != nil {
				log.Fatal("db.Exec: CREATE SCHEMA: %v", err)
			}
		}

	case setting.Database.Type.IsSQLite3():
		setting.Database.Path = ":memory:"
	}

	setting.Repository.Local.LocalCopyPath = os.TempDir()
	dir, err := os.MkdirTemp("", "prepared-forgejo")
	if err != nil {
		log.Fatal("os.MkdirTemp: %v", err)
	}
	preparedDir = dir

	setting.Repository.Local.LocalCopyPath, err = os.MkdirTemp("", "local-upload")
	if err != nil {
		log.Fatal("os.MkdirTemp: %v", err)
	}

	if err := unittest.CopyDir(path.Join(filepath.Dir(setting.AppPath), "tests/gitea-repositories-meta"), dir); err != nil {
		log.Fatal("os.RemoveAll: %v", err)
	}
	ownerDirs, err := os.ReadDir(dir)
	if err != nil {
		log.Fatal("os.ReadDir: %v", err)
	}

	for _, ownerDir := range ownerDirs {
		if !ownerDir.Type().IsDir() {
			continue
		}
		repoDirs, err := os.ReadDir(filepath.Join(dir, ownerDir.Name()))
		if err != nil {
			log.Fatal("os.ReadDir: %v", err)
		}
		for _, repoDir := range repoDirs {
			_ = os.MkdirAll(filepath.Join(dir, ownerDir.Name(), repoDir.Name(), "objects", "pack"), 0o755)
			_ = os.MkdirAll(filepath.Join(dir, ownerDir.Name(), repoDir.Name(), "objects", "info"), 0o755)
			_ = os.MkdirAll(filepath.Join(dir, ownerDir.Name(), repoDir.Name(), "refs", "heads"), 0o755)
			_ = os.MkdirAll(filepath.Join(dir, ownerDir.Name(), repoDir.Name(), "refs", "tag"), 0o755)
			_ = os.MkdirAll(filepath.Join(dir, ownerDir.Name(), repoDir.Name(), "refs", "pull"), 0o755)
		}
	}

	routers.InitWebInstalled(graceful.GetManager().HammerContext())
}

func PrepareAttachmentsStorage(t testing.TB) {
	// prepare attachments directory and files
	require.NoError(t, storage.Clean(storage.Attachments))

	s, err := storage.NewStorage(setting.LocalStorageType, &setting.Storage{
		Path: filepath.Join(filepath.Dir(setting.AppPath), "tests", "testdata", "data", "attachments"),
	})
	require.NoError(t, err)
	require.NoError(t, s.IterateObjects("", func(p string, obj storage.Object) error {
		_, err = storage.Copy(storage.Attachments, p, s, p)
		return err
	}))
}

// cancelProcesses cancels all processes of the [process.Manager].
// Returns immediately if delay is 0, otherwise wait until all processes are done
// and fails the test if it takes longer that the given delay.
func cancelProcesses(t testing.TB, delay time.Duration) {
	processManager := process.GetManager()
	processes, _ := processManager.Processes(true, true)
	for _, p := range processes {
		processManager.Cancel(p.PID)
		t.Logf("PrepareTestEnv:Process %q cancelled", p.Description)
	}
	if delay == 0 || len(processes) == 0 {
		return
	}

	start := time.Now()
	processes, _ = processManager.Processes(true, true)
	for len(processes) > 0 {
		if time.Since(start) > delay {
			t.Errorf("ERROR PrepareTestEnv: could not cancel all processes within %s", delay)
			for _, p := range processes {
				t.Logf("PrepareTestEnv:Remaining Process: %q", p.Description)
			}
			stacks := allGoroutineStacks()
			t.Errorf("All goroutine stacks during process cancellation failure:\n%s", string(stacks))
			// exit so that we don't spin in a loop executing `delay` wait over and over again when we won't be able to
			// complete tests correctly due to the environmental issue present.
			exitf("terminating test run due to unrecoverable failure")
			return
		}
		runtime.Gosched() // let the context cancellation propagate
		processes, _ = processManager.Processes(true, true)
	}
	t.Logf("PrepareTestEnv: all processes cancelled within %s", time.Since(start))
}

// allGoroutineStacks is the same as runtime/debug.Stack(), but it captures the stack of all goroutines.
func allGoroutineStacks() []byte {
	buf := make([]byte, 1024)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			return buf[:n]
		}
		buf = make([]byte, 2*len(buf))
	}
}

func PrepareGitRepoDirectory(t testing.TB) {
	setting.RepoRootPath = t.TempDir()
	require.NoError(t, unittest.CopyDir(preparedDir, setting.RepoRootPath))
}

func PrepareArtifactsStorage(t testing.TB) {
	// prepare actions artifacts directory and files
	require.NoError(t, storage.Clean(storage.ActionsArtifacts))

	s, err := storage.NewStorage(setting.LocalStorageType, &setting.Storage{
		Path: filepath.Join(filepath.Dir(setting.AppPath), "tests", "testdata", "data", "artifacts"),
	})
	require.NoError(t, err)
	require.NoError(t, s.IterateObjects("", func(p string, obj storage.Object) error {
		_, err = storage.Copy(storage.ActionsArtifacts, p, s, p)
		return err
	}))
}

func PrepareLFSStorage(t testing.TB) {
	// load LFS object fixtures
	// (LFS storage can be on any of several backends, including remote servers, so init it with the storage API)
	lfsFixtures, err := storage.NewStorage(setting.LocalStorageType, &setting.Storage{
		Path: filepath.Join(filepath.Dir(setting.AppPath), "tests/gitea-lfs-meta"),
	})
	require.NoError(t, err)
	require.NoError(t, storage.Clean(storage.LFS))
	require.NoError(t, lfsFixtures.IterateObjects("", func(path string, _ storage.Object) error {
		_, err := storage.Copy(storage.LFS, path, lfsFixtures, path)
		return err
	}))
}

func PrepareCleanPackageData(t testing.TB) {
	// clear all package data
	require.NoError(t, db.TruncateBeans(db.DefaultContext,
		&packages_model.Package{},
		&packages_model.PackageVersion{},
		&packages_model.PackageFile{},
		&packages_model.PackageBlob{},
		&packages_model.PackageProperty{},
		&packages_model.PackageBlobUpload{},
		&packages_model.PackageCleanupRule{},
	))
	require.NoError(t, storage.Clean(storage.Packages))
}

// inTestEnv keeps track if we are current inside a test environment, this is
// used to detect if testing code tries to prepare a test environment more than
// once.
var inTestEnv atomic.Bool

func PrepareTestEnv(t testing.TB, skip ...int) func() {
	deferFn := PrepareTestEnvWithPackageData(t, skip...)
	PrepareCleanPackageData(t)
	return deferFn
}

// Doesn't perform the `PrepareCleanPackageData` that `PrepareTestEnv` does...
func PrepareTestEnvWithPackageData(t testing.TB, skip ...int) func() {
	t.Helper()

	if !inTestEnv.CompareAndSwap(false, true) {
		t.Fatal("Cannot prepare a test environment if you are already in a test environment. This is a bug in your testing code.")
	}

	deferPrintCurrentTest := PrintCurrentTest(t, util.OptionalArg(skip)+1)
	deferFn := func() {
		deferPrintCurrentTest()

		if !inTestEnv.CompareAndSwap(true, false) {
			t.Fatal("Tried to leave test environment, but we are no longer in a test environment. This should not happen.")
		}
	}

	cancelProcesses(t, 30*time.Second)
	t.Cleanup(func() { cancelProcesses(t, 0) }) // cancel remaining processes in a non-blocking way

	// load database fixtures
	require.NoError(t, unittest.LoadFixtures())

	// do not add more Prepare* functions here, only call necessary ones in the related test functions
	PrepareGitRepoDirectory(t)
	PrepareLFSStorage(t)
	return deferFn
}

func PrintCurrentTest(t testing.TB, skip ...int) func() {
	t.Helper()
	return testlogger.PrintCurrentTest(t, util.OptionalArg(skip)+1)
}

// Printf takes a format and args and prints the string to os.Stdout
func Printf(format string, args ...any) {
	testlogger.Printf(format, args...)
}

// Deprecated: use forgery.CreateRepository instead
func CreateDeclarativeRepo(t *testing.T, owner *user_model.User, name string, enabledUnits, disabledUnits []unit_model.Type, files []*files_service.ChangeRepoFile) (*repo_model.Repository, string, func()) {
	t.Helper()

	opts := &forgery.CreateRepositoryOptions{
		LatestSha: new(string),
		Name:      name,
	}

	if len(files) > 0 {
		licenseData, err := options.License("CC0-1.0")
		require.NoError(t, err)
		mfs := forgery.MapFS{
			"README.md": forgery.MapFile("# " + t.Name() + "\n\nThis is a test repo created via test_utils"),
			"LICENSE":   &fstest.MapFile{Data: licenseData},
		}
		for _, f := range files {
			require.Empty(t, f.FromTreePath, f.TreePath)
			if f.Operation != "create" {
				require.Equal(t, "README.md", f.TreePath, "%q operation only expected on README.md", f.Operation)
				if f.Operation == "delete" {
					delete(mfs, f.TreePath)
					continue
				}
				require.Equal(t, "update", f.Operation, "only update/delete operations are supported on README.md")
			}
			if f.SHA != "" {
				require.Nil(t, f.ContentReader, f.TreePath)
				mfs[f.TreePath] = forgery.MapSubmodule(f.SHA)
			} else {
				require.Nil(t, f.Options, f.TreePath)

				data, err := io.ReadAll(f.ContentReader)
				require.NoError(t, err)
				mfs[f.TreePath] = &fstest.MapFile{
					Data: data,
				}
			}
		}
		opts.Files = mfs
	} else {
		opts.Files = forgery.FilesInit{}
	}

	repo := forgery.CreateRepository(t, owner, opts)
	notify.CreateRepository(t.Context(), owner, owner, repo) // CreateDeclarativeRepoWithOptions didn't call CreateRepositoryDirectly; notify manually to keep the same behavior

	for _, unitType := range enabledUnits {
		var config convert.Conversion
		if unitType == unit_model.TypePullRequests {
			config = &repo_model.PullRequestsConfig{
				AllowMerge:           true,
				AllowRebase:          true,
				AllowRebaseMerge:     true,
				AllowSquash:          true,
				AllowFastForwardOnly: true,
				AllowManualMerge:     true,
				AllowRebaseUpdate:    true,
				DefaultMergeStyle:    repo_model.MergeStyleMerge,
				DefaultUpdateStyle:   repo_model.UpdateStyleMerge,
			}
		}
		forgery.EnableRepoUnit(t, repo, unitType, config)
	}
	if disabledUnits != nil {
		forgery.DisableRepoUnits(t, repo, disabledUnits...)
	}
	return repo, *opts.LatestSha, func() {}
}

func WriteImageBody(t *testing.T, buff bytes.Buffer, filename string, body *bytes.Buffer) string {
	writer := multipart.NewWriter(body)
	defer writer.Close()
	part, err := writer.CreateFormFile("attachment", filename)
	require.NoError(t, err)
	_, err = io.Copy(part, &buff)
	require.NoError(t, err)
	return writer.FormDataContentType()
}
