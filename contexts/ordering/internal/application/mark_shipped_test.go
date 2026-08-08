package application_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/example/go-ddd-template/contexts/ordering/internal/adapter/outbound/memory"
	"github.com/example/go-ddd-template/contexts/ordering/internal/application"
	"github.com/example/go-ddd-template/contexts/ordering/internal/domain"
	"github.com/example/go-ddd-template/shared/uow"
)

// prepareShipmentFor は confirmed の注文を作り、その出荷を preparing で 1 件用意する。
func prepareShipmentFor(t *testing.T, f memFixture) string {
	t.Helper()
	orderID := placeConfirmedOrder(t, f)
	shipmentID, err := f.prepareShipment.Handle(context.Background(), orderID)
	require.NoError(t, err, "前提の出荷準備")
	return shipmentID.String()
}

func TestMarkShipped_Happy(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	f := newMemFixture(t)
	shipmentID := prepareShipmentFor(t, f)

	require.NoError(t, f.markShipped.Handle(ctx, shipmentID, "TRACK-1"), "発送")

	view, err := f.getShipment.Handle(ctx, shipmentID)
	require.NoError(t, err, "出荷照会")
	assert.Equal(t, "shipped", view.Status, "遷移後の状態")
	assert.Equal(t, "TRACK-1", view.TrackingNumber, "追跡番号")
	assert.Equal(t, 2, view.Version, "楽観的排他の版が進む")

	// ShipmentDispatched はプロセス内配信のみ。アウトボックスへは積まない（BR-S8）。
	assert.Equal(t, 1, countEvents(*f.captured, "ordering.shipment_dispatched"), "プロセス内配信")
	assert.Empty(t, filterByType(f.stores.Queued(), "ordering.shipment_dispatched"), "配送キューへは積まない")
	assert.Empty(t, filterByType(f.stores.Events(), "ordering.shipment_dispatched"), "イベントログへも積まない")
}

func TestMarkShipped_NotPreparing(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	f := newMemFixture(t)
	shipmentID := prepareShipmentFor(t, f)
	require.NoError(t, f.markShipped.Handle(ctx, shipmentID, "TRACK-1"), "1 回目")

	err := f.markShipped.Handle(ctx, shipmentID, "TRACK-2")

	require.ErrorIs(t, err, domain.ErrShipmentNotPreparing, "番兵")
	view, getErr := f.getShipment.Handle(ctx, shipmentID)
	require.NoError(t, getErr, "出荷照会")
	assert.Equal(t, "TRACK-1", view.TrackingNumber, "追跡番号は上書きされない")
	assert.Equal(t, 2, view.Version, "版も進まない")
}

func TestMarkShipped_NotFound(t *testing.T) {
	t.Parallel()

	f := newMemFixture(t)

	err := f.markShipped.Handle(context.Background(), "MISSING-SHIPMENT", "TRACK-1")

	require.ErrorIs(t, err, domain.ErrShipmentNotFound, "番兵")
}

func TestMarkShipped_InvalidInput(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("異常系: 出荷 ID が空白のみなら 422 の入力検証エラーになる", func(t *testing.T) {
		t.Parallel()

		f := newMemFixture(t)

		err := f.markShipped.Handle(ctx, "   ", "TRACK-1")

		require.ErrorIs(t, err, domain.ErrInvalidShipmentID, "番兵")
		var ve *application.ValidationError
		require.ErrorAs(t, err, &ve, "入力検証エラーとして位置づけられる")
		require.Len(t, ve.Violations, 1, "違反は 1 件")
		assert.Equal(t, "ShipmentId", ve.Violations[0].Path, "入力 DTO 上のパス")
		assert.Equal(t, "invalid_shipment_id", ve.Violations[0].Code, "code")
	})

	t.Run("異常系: 追跡番号が空白のみなら 422 の入力検証エラーになる", func(t *testing.T) {
		t.Parallel()

		f := newMemFixture(t)
		shipmentID := prepareShipmentFor(t, f)

		err := f.markShipped.Handle(ctx, shipmentID, "   ")

		require.ErrorIs(t, err, domain.ErrInvalidTrackingNumber, "番兵")
		var ve *application.ValidationError
		require.ErrorAs(t, err, &ve, "入力検証エラーとして位置づけられる")
		require.Len(t, ve.Violations, 1, "違反は 1 件")
		assert.Equal(t, "TrackingNumber", ve.Violations[0].Path, "入力 DTO 上のパス")
		assert.Equal(t, "invalid_tracking_number", ve.Violations[0].Code, "code")
	})
}

// 楽観的排他制御が実際に働くことを、再試行上限を超える衝突で確かめる。
func TestMarkShipped_ConcurrencyConflict(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	orderRows := memory.NewOrderRows()
	shipmentRows := memory.NewShipmentRows()
	stores := memory.NewStores()
	work := memory.NewUnitOfWork(orderRows, shipmentRows, stores)

	// 前提の注文と出荷は、衝突を注入していない素の UoW で作る。
	base := newMemFixtureWith(t, work, orderRows, shipmentRows, stores)
	shipmentID := prepareShipmentFor(t, base)

	// 再試行上限（既定 3）を超える回数だけ衝突を注入し、UoW を必ず失敗させる。
	flaky := &flakyUoW{inner: work, failsLeft: 10}
	f := newMemFixtureWith(t, flaky, orderRows, shipmentRows, stores)

	err := f.markShipped.Handle(ctx, shipmentID, "TRACK-1")

	require.ErrorIs(t, err, uow.ErrConcurrencyConflict, "再試行上限を超えると衝突が伝播する")
	view, getErr := base.getShipment.Handle(ctx, shipmentID)
	require.NoError(t, getErr, "出荷照会")
	assert.Equal(t, "preparing", view.Status, "ロールバックされて状態は preparing のまま")
	assert.Equal(t, 1, view.Version, "版も据え置き")
}
