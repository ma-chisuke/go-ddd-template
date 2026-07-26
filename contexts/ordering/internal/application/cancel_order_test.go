package application_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/example/go-ddd-template/contexts/ordering/internal/application"
	"github.com/example/go-ddd-template/contexts/ordering/internal/domain"
)

// これらは本物のインメモリアダプタで取消・照会の通し挙動（同一 tx の原子性・version 増分）を
// 検証するテスト群。ポート単体の相互作用（Load/Save/Enqueue のルーティング）は
// [port_interaction_test.go] で gomock により別途検証する。

// placeOne は happy path で注文を 1 件作成し、その ID を返す（取消系テストの前準備）。
// 前準備の予約はちょうど 1 回成功する。
func placeOne(t *testing.T, f memFixture) string {
	t.Helper()
	f.reserver.EXPECT().Reserve(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	id, err := f.place.Handle(context.Background(), sampleInput())
	require.NoError(t, err, "前準備の注文作成に失敗")
	return id.String()
}

func TestCancelOrder_EnqueuesOrderCancelledSameTx(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	f := newMemFixture(t)
	id := placeOne(t, f)

	require.NoError(t, f.cancel.Handle(ctx, id))

	// 注文が Cancelled・version 2 で保存されている。
	view, err := f.get.Handle(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "cancelled", view.Status)
	assert.Equal(t, 2, view.Version)

	// 保存と同一 tx で OrderCancelled が outbox に積まれている（両方存在＝原子的コミット）。
	cancels := filterByType(f.stores.Queued(), application.MessageTypeOrderCancelled)
	require.Len(t, cancels, 1)
	assert.Equal(t, id, decodeReservationRef(t, cancels[0].Payload))

	// 恒久イベントログにも同一 tx で記録されている。
	logged := filterByType(f.stores.Events(), application.MessageTypeOrderCancelled)
	require.Len(t, logged, 1, "イベントログにも記録される")
	assert.Equal(t, cancels[0].ID, logged[0].ID, "イベントログの ID は outbox と同じ")
}

func TestCancelOrder_NotConfirmed(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	f := newMemFixture(t)
	id := placeOne(t, f)

	require.NoError(t, f.cancel.Handle(ctx, id), "1 回目の取消に失敗")
	// 取消済みの注文を再度取り消すと ErrOrderNotConfirmed。
	require.ErrorIs(t, f.cancel.Handle(ctx, id), domain.ErrOrderNotConfirmed)
}

func TestGetOrder_NotFound(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	f := newMemFixture(t)

	_, err := f.get.Handle(ctx, "UNKNOWN-ORDER")
	require.ErrorIs(t, err, domain.ErrOrderNotFound)
}
