// Copyright 2022 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package db_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"forgejo.org/models/db"
	issues_model "forgejo.org/models/issues"
	"forgejo.org/models/unittest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInTransaction(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	assert.False(t, db.InTransaction(db.DefaultContext))
	require.NoError(t, db.WithTx(db.DefaultContext, func(ctx context.Context) error {
		assert.True(t, db.InTransaction(ctx))
		return nil
	}))

	ctx, committer, err := db.TxContext(db.DefaultContext)
	require.NoError(t, err)
	defer committer.Close()
	assert.True(t, db.InTransaction(ctx))
	require.NoError(t, db.WithTx(ctx, func(ctx context.Context) error {
		assert.True(t, db.InTransaction(ctx))
		return nil
	}))
}

func TestTxContext(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	{ // create new transaction
		ctx, committer, err := db.TxContext(db.DefaultContext)
		require.NoError(t, err)
		assert.True(t, db.InTransaction(ctx))
		require.NoError(t, committer.Commit())
	}

	{ // reuse the transaction created by TxContext and commit it
		ctx, committer, err := db.TxContext(db.DefaultContext)
		engine := db.GetEngine(ctx)
		require.NoError(t, err)
		assert.True(t, db.InTransaction(ctx))
		{
			ctx, committer, err := db.TxContext(ctx)
			require.NoError(t, err)
			assert.True(t, db.InTransaction(ctx))
			assert.Equal(t, engine, db.GetEngine(ctx))
			require.NoError(t, committer.Commit())
		}
		require.NoError(t, committer.Commit())
	}

	{ // reuse the transaction created by TxContext and close it
		ctx, committer, err := db.TxContext(db.DefaultContext)
		engine := db.GetEngine(ctx)
		require.NoError(t, err)
		assert.True(t, db.InTransaction(ctx))
		{
			ctx, committer, err := db.TxContext(ctx)
			require.NoError(t, err)
			assert.True(t, db.InTransaction(ctx))
			assert.Equal(t, engine, db.GetEngine(ctx))
			require.NoError(t, committer.Close())
		}
		require.NoError(t, committer.Close())
	}

	{ // reuse the transaction created by WithTx
		require.NoError(t, db.WithTx(db.DefaultContext, func(ctx context.Context) error {
			assert.True(t, db.InTransaction(ctx))
			{
				ctx, committer, err := db.TxContext(ctx)
				require.NoError(t, err)
				assert.True(t, db.InTransaction(ctx))
				require.NoError(t, committer.Commit())
			}
			return nil
		}))
	}

	t.Run("Reuses parent context", func(t *testing.T) {
		type unique struct{}

		ctx := context.WithValue(db.DefaultContext, unique{}, "yes!")
		assert.False(t, db.InTransaction(ctx))

		require.NoError(t, db.WithTx(ctx, func(ctx context.Context) error {
			assert.Equal(t, "yes!", ctx.Value(unique{}))
			return nil
		}))
	})
}

func TestAfterTx(t *testing.T) {
	tests := []struct {
		executionMode string
		rollback      bool
	}{
		{
			executionMode: "NoTx",
		},
		{
			executionMode: "WithTx",
		},
		{
			executionMode: "WithTxNested",
		},
		{
			executionMode: "WithTx",
			rollback:      true,
		},
		{
			executionMode: "WithTxNested",
			rollback:      true,
		},
		{
			executionMode: "TxContext",
		},
		{
			executionMode: "TxContextNested",
		},
		{
			executionMode: "TxContext",
			rollback:      true,
		},
		{
			executionMode: "TxContextNested",
			rollback:      true,
		},
	}

	for _, tc := range tests {
		t.Run(fmt.Sprintf("%s/%v", tc.executionMode, tc.rollback), func(t *testing.T) {
			require.NoError(t, unittest.PrepareTestDatabase())
			ctx := t.Context()

			var err error
			var countBefore, countAfter, hookCount int64

			countBefore, err = db.GetEngine(ctx).Count(&issues_model.PullRequest{})
			require.NoError(t, err)

			sut := func(ctx context.Context) {
				_, err = db.GetEngine(ctx).Insert(
					&issues_model.PullRequest{IssueID: 2, BaseRepoID: 1, HeadRepoID: 1000})
				require.NoError(t, err)
				db.AfterTx(ctx, func() {
					countAfter, err = db.GetEngine(ctx).Count(&issues_model.PullRequest{})
					require.NoError(t, err)
					assert.False(t, db.InTransaction(ctx))
					hookCount++
				})
			}

			switch tc.executionMode {
			case "NoTx":
				sut(ctx)
			case "WithTx":
				db.WithTx(ctx, func(ctx context.Context) error {
					sut(ctx)
					if tc.rollback {
						return errors.New("rollback")
					}
					return nil
				})
			case "WithTxNested":
				db.WithTx(ctx, func(ctx context.Context) error {
					return db.WithTx(ctx, func(ctx context.Context) error {
						sut(ctx)
						if tc.rollback {
							return errors.New("rollback")
						}
						return nil
					})
				})
			case "TxContext":
				txCtx, committer, err := db.TxContext(ctx)
				require.NoError(t, err)
				sut(txCtx)
				if !tc.rollback {
					err = committer.Commit()
					require.NoError(t, err)
				}
				committer.Close()
			case "TxContextNested":
				txCtx1, committer1, err := db.TxContext(ctx)
				require.NoError(t, err)
				txCtx2, committer2, err := db.TxContext(txCtx1)
				require.NoError(t, err)
				sut(txCtx2)
				err = committer2.Commit()
				require.NoError(t, err)
				committer2.Close()
				if !tc.rollback {
					err = committer1.Commit()
					require.NoError(t, err)
				}
				committer1.Close()
			default:
				t.Fatalf("unexpected execution mode: %q", tc.executionMode)
			}

			if tc.rollback {
				assert.EqualValues(t, 0, hookCount)
				assert.EqualValues(t, 0, countAfter)
			} else {
				assert.EqualValues(t, 1, hookCount)
				assert.Equal(t, countBefore+1, countAfter)
			}
		})
	}
}

func TestRetryTx(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		err := db.RetryTx(t.Context(), db.RetryConfig{AttemptCount: 1}, func(ctx context.Context) error {
			assert.True(t, db.InTransaction(ctx))
			return nil
		})
		require.NoError(t, err)
	})

	t.Run("fail constantly", func(t *testing.T) {
		attemptCount := 0
		testError := errors.New("hello")
		err := db.RetryTx(t.Context(), db.RetryConfig{
			AttemptCount: 2,
			ErrorIs:      []error{testError},
		}, func(ctx context.Context) error {
			attemptCount++
			return testError
		})
		require.ErrorIs(t, err, testError)
		require.ErrorContains(t, err, "2 attempts")
		assert.Equal(t, 2, attemptCount)
	})

	t.Run("fail w/ non retriable error", func(t *testing.T) {
		attemptCount := 0
		testError := errors.New("hello")
		err := db.RetryTx(t.Context(), db.RetryConfig{
			AttemptCount: 2,
			ErrorIs:      []error{},
		}, func(ctx context.Context) error {
			attemptCount++
			return testError
		})
		require.ErrorIs(t, err, testError)
		assert.Equal(t, 1, attemptCount)
	})

	t.Run("succeed on retry", func(t *testing.T) {
		attemptCount := 0
		testError := errors.New("hello")
		err := db.RetryTx(t.Context(), db.RetryConfig{
			AttemptCount: 2,
			ErrorIs:      []error{testError},
		}, func(ctx context.Context) error {
			attemptCount++
			if attemptCount == 1 {
				return testError
			}
			return nil
		})
		require.NoError(t, err)
		assert.Equal(t, 2, attemptCount)
	})

	t.Run("nested", func(t *testing.T) {
		attemptCount := 0
		testError := errors.New("hello")
		err := db.RetryTx(t.Context(), db.RetryConfig{
			AttemptCount: 2,
		}, func(ctx context.Context) error {
			attemptCount++
			return db.RetryTx(ctx, db.RetryConfig{
				AttemptCount: 2,
				ErrorIs:      []error{testError},
			}, func(ctx context.Context) error {
				if attemptCount == 2 {
					return nil
				}
				return testError
			})
		})

		require.NoError(t, err)
		assert.Equal(t, 2, attemptCount)
	})

	t.Run("inner RetryTx decides on error", func(t *testing.T) {
		attemptCount := 0
		testError := errors.New("hello")
		err := db.RetryTx(t.Context(), db.RetryConfig{
			AttemptCount: 2,
			ErrorIs:      []error{},
		}, func(ctx context.Context) error {
			attemptCount++
			return db.RetryTx(ctx, db.RetryConfig{
				AttemptCount: 2,
			}, func(ctx context.Context) error {
				if attemptCount == 2 {
					return nil
				}
				return testError
			})
		})

		require.ErrorIs(t, err, testError)
		assert.Equal(t, 1, attemptCount)
	})
}
