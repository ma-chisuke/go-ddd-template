package application_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/example/go-ddd-template/contexts/ordering/internal/domain"
)

// prepareOne は happy path で出荷を 1 件準備し、その ID を返す（発送系テストの前準備）。
func prepareOne(t *testing.T, f memFixture) string {
	t.Helper()
	orderID := placeOne(t, f)
	view, err := f.prepareShip.Handle(context.Background(), orderID)
	require.NoError(t, err, "前準備の出荷準備に失敗")
	return view.ID
}

func TestMarkShipped_TransitionsAndDispatchesEvent(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	f := newMemFixture(t)
	shipmentID := prepareOne(t, f)
	// 前準備（注文作成）が積んだメッセージ数。発送済み化がこれを増やさないことを後で確かめる。
	queuedBefore := len(f.stores.Queued())

	view, err := f.markShipped.Handle(ctx, shipmentID, "TRACK-1")
	require.NoError(t, err)
	assert.Equal(t, "shipped", view.Status, "遷移後の状態")
	assert.Equal(t, "TRACK-1", view.TrackingNumber, "追跡番号")
	assert.Equal(t, 2, view.Version, "更新で version が増える")

	// 確定ストアにも反映されている（コミット後の読み取り）。
	got, err := f.getShip.Handle(ctx, shipmentID)
	require.NoError(t, err)
	assert.Equal(t, view, got, "照会結果は発送直後の状態と一致する")

	// プロセス内イベントとして配信される（アウトボックスには積まない）。
	assert.Equal(t, 1, countEvents(*f.captured, "ordering.shipment_dispatched"),
		"ShipmentDispatched がディスパッチャへ配信される")
	assert.Len(t, f.stores.Queued(), queuedBefore, "クロスコンテキスト送信は行わない（配送キューは増えない）")
}

func TestMarkShipped_AlreadyShipped(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	f := newMemFixture(t)
	shipmentID := prepareOne(t, f)
	_, err := f.markShipped.Handle(ctx, shipmentID, "TRACK-1")
	require.NoError(t, err, "前準備の発送に失敗")

	_, err = f.markShipped.Handle(ctx, shipmentID, "TRACK-2")
	require.ErrorIs(t, err, domain.ErrShipmentNotPreparing, "冪等ではなくエラーを返す")

	// 状態が上書きされていない（ロールバックされている）。
	got, err := f.getShip.Handle(ctx, shipmentID)
	require.NoError(t, err)
	assert.Equal(t, "TRACK-1", got.TrackingNumber, "追跡番号は最初のもののまま")
	assert.Equal(t, 2, got.Version, "version も増えていない")
}

func TestMarkShipped_ShipmentNotFound(t *testing.T) {
	t.Parallel()

	f := newMemFixture(t)

	_, err := f.markShipped.Handle(context.Background(), "SHIP-missing", "TRACK-1")
	require.ErrorIs(t, err, domain.ErrShipmentNotFound)
}

func TestMarkShipped_InvalidInput(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	f := newMemFixture(t)
	shipmentID := prepareOne(t, f)

	t.Run("異常系: 出荷 ID が空白のみなら ErrInvalidShipmentID", func(t *testing.T) {
		t.Parallel()

		_, err := f.markShipped.Handle(ctx, "   ", "TRACK-1")
		require.ErrorIs(t, err, domain.ErrInvalidShipmentID)
	})

	t.Run("異常系: 追跡番号が空白のみなら ErrInvalidTrackingNumber", func(t *testing.T) {
		t.Parallel()

		_, err := f.markShipped.Handle(ctx, shipmentID, "   ")
		require.ErrorIs(t, err, domain.ErrInvalidTrackingNumber)
	})
}

func TestGetShipment_NotFoundAndInvalidID(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	f := newMemFixture(t)

	_, err := f.getShip.Handle(ctx, "SHIP-missing")
	require.ErrorIs(t, err, domain.ErrShipmentNotFound)

	_, err = f.getShip.Handle(ctx, "   ")
	require.ErrorIs(t, err, domain.ErrInvalidShipmentID)
}
