package uow_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/example/go-ddd-template/shared/uow"
)

// fakeRepos はテスト用の空のリポジトリ束。
type fakeRepos struct{}

// fakeUoW はコールバックをそのまま実行するだけの UnitOfWork。
// Within の呼び出し回数を数える。
type fakeUoW struct {
	withinCalls int
}

func (u *fakeUoW) Within(ctx context.Context, fn func(ctx context.Context, r fakeRepos) error) error {
	u.withinCalls++
	return fn(ctx, fakeRepos{})
}

func TestRun_SucceedsFirstTry(t *testing.T) {
	ctx := context.Background()
	unit := &fakeUoW{}
	exec := uow.NewExecutor(uow.WithBaseBackoff(0))

	calls := 0
	err := uow.Run(ctx, exec, unit, func(ctx context.Context, _ fakeRepos) error {
		calls++
		return nil
	})
	require.NoError(t, err, "想定外のエラー")
	assert.Equal(t, 1, calls, "cmd 呼び出し回数")
	assert.Equal(t, 1, unit.withinCalls, "Within 呼び出し回数")
}

func TestRun_RetriesOnConflictThenSucceeds(t *testing.T) {
	ctx := context.Background()
	unit := &fakeUoW{}
	exec := uow.NewExecutor(uow.WithMaxAttempts(3), uow.WithBaseBackoff(0))

	calls := 0
	err := uow.Run(ctx, exec, unit, func(ctx context.Context, _ fakeRepos) error {
		calls++
		if calls < 3 {
			return uow.ErrConcurrencyConflict
		}
		return nil
	})
	require.NoError(t, err, "想定外のエラー")
	assert.Equal(t, 3, calls, "cmd 呼び出し回数")
}

func TestRun_GivesUpAfterMaxAttempts(t *testing.T) {
	ctx := context.Background()
	unit := &fakeUoW{}
	exec := uow.NewExecutor(uow.WithMaxAttempts(2), uow.WithBaseBackoff(0))

	calls := 0
	err := uow.Run(ctx, exec, unit, func(ctx context.Context, _ fakeRepos) error {
		calls++
		return uow.ErrConcurrencyConflict
	})
	require.ErrorIs(t, err, uow.ErrConcurrencyConflict, "エラーは ErrConcurrencyConflict であるべき")
	assert.Equal(t, 2, calls, "cmd 呼び出し回数")
}

func TestRun_NonConflictErrorReturnsImmediately(t *testing.T) {
	ctx := context.Background()
	unit := &fakeUoW{}
	exec := uow.NewExecutor(uow.WithMaxAttempts(5), uow.WithBaseBackoff(0))

	sentinel := errors.New("業務エラー")
	calls := 0
	err := uow.Run(ctx, exec, unit, func(ctx context.Context, _ fakeRepos) error {
		calls++
		return sentinel
	})
	require.ErrorIs(t, err, sentinel, "エラーは sentinel であるべき")
	assert.Equal(t, 1, calls, "cmd 呼び出し回数（衝突以外は再試行しない）")
}

func TestRun_ContextCancelledDuringBackoff(t *testing.T) {
	// 既にキャンセル済みの context を渡すと、衝突後のバックオフ待機で中断される。
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	unit := &fakeUoW{}
	exec := uow.NewExecutor(uow.WithMaxAttempts(3), uow.WithBaseBackoff(50*time.Millisecond))

	err := uow.Run(ctx, exec, unit, func(ctx context.Context, _ fakeRepos) error {
		return uow.ErrConcurrencyConflict
	})
	require.ErrorIs(t, err, context.Canceled, "エラーは context.Canceled であるべき")
}

func TestWithMaxAttempts_ClampedToOne(t *testing.T) {
	ctx := context.Background()
	unit := &fakeUoW{}
	// 0 以下は 1 に丸められる。
	exec := uow.NewExecutor(uow.WithMaxAttempts(0), uow.WithBaseBackoff(0))

	calls := 0
	_ = uow.Run(ctx, exec, unit, func(ctx context.Context, _ fakeRepos) error {
		calls++
		return uow.ErrConcurrencyConflict
	})
	assert.Equal(t, 1, calls, "cmd 呼び出し回数")
}
