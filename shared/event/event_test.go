package event_test

import (
	"context"
	"errors"
	"testing"

	"github.com/example/go-ddd-template/shared/event"
)

// fakeEvent はテスト用の最小イベント。
type fakeEvent struct {
	name string
}

func (e fakeEvent) EventName() string { return e.name }

func TestInProcess_DispatchByName(t *testing.T) {
	ctx := context.Background()
	d := event.NewInProcess()

	var gotA, gotB int
	d.Register("a", func(_ context.Context, _ event.Event) error { gotA++; return nil })
	d.Register("b", func(_ context.Context, _ event.Event) error { gotB++; return nil })

	// a を 2 件、b を 1 件配信する。
	err := d.Dispatch(ctx, fakeEvent{"a"}, fakeEvent{"a"}, fakeEvent{"b"})
	if err != nil {
		t.Fatalf("想定外のエラー: %v", err)
	}
	if gotA != 2 || gotB != 1 {
		t.Fatalf("配信回数が不正: a=%d b=%d, want a=2 b=1", gotA, gotB)
	}
}

func TestInProcess_MultipleHandlersSameName(t *testing.T) {
	ctx := context.Background()
	d := event.NewInProcess()

	calls := 0
	d.Register("x", func(_ context.Context, _ event.Event) error { calls++; return nil })
	d.Register("x", func(_ context.Context, _ event.Event) error { calls++; return nil })

	if err := d.Dispatch(ctx, fakeEvent{"x"}); err != nil {
		t.Fatalf("想定外のエラー: %v", err)
	}
	if calls != 2 {
		t.Fatalf("同一種別の全ハンドラが呼ばれるべき: calls=%d, want 2", calls)
	}
}

func TestInProcess_UnregisteredNameIsNoop(t *testing.T) {
	ctx := context.Background()
	d := event.NewInProcess()
	// 購読者のいない種別は素通り(エラーにならない)。
	if err := d.Dispatch(ctx, fakeEvent{"unknown"}); err != nil {
		t.Fatalf("未登録種別はエラーにならないべき: %v", err)
	}
}

func TestInProcess_HandlerErrorStopsDispatch(t *testing.T) {
	ctx := context.Background()
	d := event.NewInProcess()

	sentinel := errors.New("ハンドラ失敗")
	first := 0
	d.Register("e", func(_ context.Context, _ event.Event) error { first++; return sentinel })
	second := 0
	d.Register("e", func(_ context.Context, _ event.Event) error { second++; return nil })

	err := d.Dispatch(ctx, fakeEvent{"e"})
	if !errors.Is(err, sentinel) {
		t.Fatalf("エラー = %v, want sentinel", err)
	}
	// 最初のハンドラでエラーが出たら以降は呼ばれない。
	if first != 1 || second != 0 {
		t.Fatalf("中断挙動が不正: first=%d second=%d", first, second)
	}
}
