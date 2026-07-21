package event_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
	require.NoError(t, err, "想定外のエラー")
	assert.Equal(t, 2, gotA, "a の配信回数")
	assert.Equal(t, 1, gotB, "b の配信回数")
}

func TestInProcess_MultipleHandlersSameName(t *testing.T) {
	ctx := context.Background()
	d := event.NewInProcess()

	calls := 0
	d.Register("x", func(_ context.Context, _ event.Event) error { calls++; return nil })
	d.Register("x", func(_ context.Context, _ event.Event) error { calls++; return nil })

	require.NoError(t, d.Dispatch(ctx, fakeEvent{"x"}), "想定外のエラー")
	assert.Equal(t, 2, calls, "同一種別の全ハンドラが呼ばれるべき")
}

func TestInProcess_UnregisteredNameIsNoop(t *testing.T) {
	ctx := context.Background()
	d := event.NewInProcess()
	// 購読者のいない種別は素通り(エラーにならない)。
	require.NoError(t, d.Dispatch(ctx, fakeEvent{"unknown"}), "未登録種別はエラーにならないべき")
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
	require.ErrorIs(t, err, sentinel, "エラーは sentinel であるべき")
	// 最初のハンドラでエラーが出たら以降は呼ばれない。
	assert.Equal(t, 1, first, "最初のハンドラは 1 回呼ばれる")
	assert.Equal(t, 0, second, "以降のハンドラは呼ばれない")
}
