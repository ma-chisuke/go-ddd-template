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

// placeConfirmedOrder は confirmed の注文を 1 件作り、その ID を返す（出荷の前提条件）。
func placeConfirmedOrder(t *testing.T, f memFixture) string {
	t.Helper()
	f.reserver.EXPECT().Reserve(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	id, err := f.place.Handle(context.Background(), sampleInput())
	require.NoError(t, err, "前提の注文作成")
	return id.String()
}

func TestPrepareShipment_Happy(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	f := newMemFixture(t)
	orderID := placeConfirmedOrder(t, f)

	shipmentID, err := f.prepareShipment.Handle(ctx, orderID)

	require.NoError(t, err, "出荷準備")
	require.False(t, shipmentID.IsZero(), "出荷 ID が採番される")

	view, err := f.getShipment.Handle(ctx, shipmentID.String())
	require.NoError(t, err, "出荷照会")
	assert.Equal(t, "preparing", view.Status, "初期状態")
	assert.Equal(t, orderID, view.OrderID, "注文を識別子で参照する")
	assert.Empty(t, view.TrackingNumber, "preparing の追跡番号は空")
	assert.Equal(t, 1, view.Version, "永続化済みの version は 1")
}

// 出荷は注文とは別の集約なので、注文自身は出荷準備で一切書き換わらない
// （1 トランザクションで書き込む集約ルートは 1 つ）。
func TestPrepareShipment_DoesNotTouchTheOrder(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	f := newMemFixture(t)
	orderID := placeConfirmedOrder(t, f)

	before, err := f.get.Handle(ctx, orderID)
	require.NoError(t, err, "前の注文状態")

	_, err = f.prepareShipment.Handle(ctx, orderID)
	require.NoError(t, err, "出荷準備")

	after, err := f.get.Handle(ctx, orderID)
	require.NoError(t, err, "後の注文状態")
	assert.Equal(t, before.Version, after.Version, "注文の版は据え置き（書き込んでいない）")
	assert.Equal(t, before.Status, after.Status, "注文の状態も不変")
}

func TestPrepareShipment_OrderNotConfirmed(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	f := newMemFixture(t)
	orderID := placeConfirmedOrder(t, f)
	require.NoError(t, f.cancel.Handle(ctx, orderID), "前提の取消")

	_, err := f.prepareShipment.Handle(ctx, orderID)

	require.ErrorIs(t, err, application.ErrOrderNotConfirmedForShipment, "番兵")
	// 注文コンテキスト自身の番兵であり、注文の状態遷移エラーとは別物である。
	assert.NotErrorIs(t, err, domain.ErrOrderNotConfirmed, "取消の番兵とは区別する")
}

func TestPrepareShipment_OrderNotFound(t *testing.T) {
	t.Parallel()

	f := newMemFixture(t)

	_, err := f.prepareShipment.Handle(context.Background(), "MISSING-ORDER")

	require.ErrorIs(t, err, domain.ErrOrderNotFound, "番兵")
}

func TestPrepareShipment_InvalidOrderID(t *testing.T) {
	t.Parallel()

	f := newMemFixture(t)

	_, err := f.prepareShipment.Handle(context.Background(), "   ")

	require.ErrorIs(t, err, domain.ErrInvalidOrderID, "番兵")
	var ve *application.ValidationError
	require.ErrorAs(t, err, &ve, "入力検証エラーとして位置づけられる")
	require.Len(t, ve.Violations, 1, "違反は 1 件")
	assert.Equal(t, "OrderId", ve.Violations[0].Path, "入力 DTO 上のパス")
	assert.Equal(t, "invalid_order_id", ve.Violations[0].Code, "code")
}
