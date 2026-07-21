package uow_test

import (
	"context"
	"errors"
	"testing"
	"time"

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
	if err != nil {
		t.Fatalf("想定外のエラー: %v", err)
	}
	if calls != 1 || unit.withinCalls != 1 {
		t.Fatalf("呼び出し回数が不正: cmd=%d within=%d", calls, unit.withinCalls)
	}
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
	if err != nil {
		t.Fatalf("想定外のエラー: %v", err)
	}
	if calls != 3 {
		t.Fatalf("cmd 呼び出し回数 = %d, want 3", calls)
	}
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
	if !errors.Is(err, uow.ErrConcurrencyConflict) {
		t.Fatalf("エラー = %v, want ErrConcurrencyConflict", err)
	}
	if calls != 2 {
		t.Fatalf("cmd 呼び出し回数 = %d, want 2", calls)
	}
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
	if !errors.Is(err, sentinel) {
		t.Fatalf("エラー = %v, want sentinel", err)
	}
	if calls != 1 {
		t.Fatalf("cmd 呼び出し回数 = %d, want 1（衝突以外は再試行しない）", calls)
	}
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
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("エラー = %v, want context.Canceled", err)
	}
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
	if calls != 1 {
		t.Fatalf("cmd 呼び出し回数 = %d, want 1", calls)
	}
}
