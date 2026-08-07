package application_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/example/go-ddd-template/contexts/ordering/internal/application"
	"github.com/example/go-ddd-template/contexts/ordering/internal/domain"
)

// 出荷は注文と別の集約ルートである。これらは本物のインメモリアダプタで、出荷の準備と
// 発送済み化の通し挙動（トランザクション境界・version 増分・イベント配信）を検証する。

func TestPrepareShipment_CreatesPreparingShipmentForConfirmedOrder(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	f := newMemFixture(t)
	orderID := placeOne(t, f)
	// 前準備の注文作成が積んだメッセージ数。出荷の準備がこれを増やさないことを後で確かめる。
	queuedBefore := len(f.stores.Queued())

	view, err := f.prepareShip.Handle(ctx, orderID)
	require.NoError(t, err)
	assert.Equal(t, "preparing", view.Status, "初期状態")
	assert.Equal(t, orderID, view.OrderID, "注文は識別子で参照する")
	assert.Empty(t, view.TrackingNumber, "preparing の間は追跡番号が無い")
	assert.Equal(t, 1, view.Version, "永続化されたので version は 1")

	// 照会経路（UoW を経由しない読み取り）でも同じ状態が読める。
	got, err := f.getShip.Handle(ctx, view.ID)
	require.NoError(t, err)
	assert.Equal(t, view, got, "照会結果は準備直後の状態と一致する")

	// **注文の側は一切変わっていない。** 出荷は注文を識別子で参照するだけで、
	// 同一トランザクションで注文を書かない（Repos.Orders() を使わない）。
	order, err := f.get.Handle(ctx, orderID)
	require.NoError(t, err)
	assert.Equal(t, 1, order.Version, "注文の version は据え置き")
	assert.Equal(t, "confirmed", order.Status, "注文の状態は据え置き")

	// 出荷の準備ではクロスコンテキストへの送信が発生しない（在庫は出荷を知る必要がない）。
	assert.Len(t, f.stores.Queued(), queuedBefore, "配送キューは増えない")
}

func TestPrepareShipment_OrderNotFound(t *testing.T) {
	t.Parallel()

	f := newMemFixture(t)

	_, err := f.prepareShip.Handle(context.Background(), "ORDER-missing")
	require.ErrorIs(t, err, domain.ErrOrderNotFound)
}

func TestPrepareShipment_OrderNotConfirmed(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	f := newMemFixture(t)
	orderID := placeOne(t, f)
	require.NoError(t, f.cancel.Handle(ctx, orderID), "前準備の取消に失敗")

	_, err := f.prepareShip.Handle(ctx, orderID)
	require.ErrorIs(t, err, application.ErrOrderNotConfirmedForShipment,
		"確定状態でない注文には出荷を準備できない")
}

func TestPrepareShipment_InvalidOrderID(t *testing.T) {
	t.Parallel()

	f := newMemFixture(t)

	_, err := f.prepareShip.Handle(context.Background(), "   ")
	require.ErrorIs(t, err, domain.ErrInvalidOrderID)
}
