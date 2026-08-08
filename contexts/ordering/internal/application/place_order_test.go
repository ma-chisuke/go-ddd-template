package application_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/example/go-ddd-template/contexts/ordering/internal/adapter/outbound/memory"
	"github.com/example/go-ddd-template/contexts/ordering/internal/application"
	"github.com/example/go-ddd-template/contexts/ordering/internal/domain"
	"github.com/example/go-ddd-template/contexts/ordering/port"
)

// これらは本物のインメモリアダプタ（永続化・トランザクション・イベント配信）で通しに検証しつつ、
// 外部同期呼び出しの在庫予約（ACL ポート）だけを gomock の StockReserver モックへ差し替える
// テスト群。予約の応答と呼び出し回数はモックの EXPECT で厳密に縛る。純粋なポート相互作用
// （Save/Enqueue のルーティング）だけを見るテストは [port_interaction_test.go] にある。

func TestPlaceOrder_Happy(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	f := newMemFixture(t)

	// フェーズ 1: 予約はちょうど 1 回、翻訳済み DTO（SKU-A×3）で呼ばれる。ref は生成された
	// 注文 ID と一致するはずなので、引数を捕捉して後で突き合わせる。
	var gotRef string
	f.reserver.EXPECT().
		Reserve(gomock.Any(), gomock.Any(), []port.ReserveLine{{SKU: "SKU-A", Qty: 3}}).
		DoAndReturn(func(_ context.Context, ref string, _ []port.ReserveLine) error {
			gotRef = ref
			return nil
		})

	id, err := f.place.Handle(ctx, sampleInput())
	require.NoError(t, err)
	assert.Equal(t, id.String(), gotRef, "予約参照は注文 ID と一致するべき")

	// フェーズ 2: 注文が Confirmed・version 1 で保存されている。
	view, err := f.get.Handle(ctx, id.String())
	require.NoError(t, err)
	assert.Equal(t, "confirmed", view.Status)
	assert.Equal(t, 1, view.Version)
	assert.Equal(t, int64(3600), view.TotalAmount)
	assert.Equal(t, "JPY", view.TotalCurrency)

	// 同一 tx で ConfirmReservation コマンドが outbox に積まれ、reservation_ref が注文 ID と一致する。
	confirms := filterByType(f.stores.Queued(), application.MessageTypeConfirmReservation)
	require.Len(t, confirms, 1)
	assert.Equal(t, id.String(), decodeReservationRef(t, confirms[0].Payload))

	// 同じ tx で恒久イベントログにも記録される（配送キューとイベントログは同一コミット）。
	logged := filterByType(f.stores.Events(), application.MessageTypeConfirmReservation)
	require.Len(t, logged, 1, "イベントログにも 1 件記録される")
	assert.Equal(t, confirms[0].ID, logged[0].ID, "イベントログの ID は outbox と同じ")

	// OrderPlaced はコミット後にプロセス内配信されている。
	assert.Equal(t, 1, countEvents(*f.captured, "ordering.order_placed"))
}

// TestPlaceOrder_EventLogWrittenInSameTx は、注文集約の保存・配送キューへの投入・
// 恒久イベントログへの記録の 3 者がひとつのトランザクションで確定することを、
// ロールバック時とコミット時の両方で確認する（FR-4 / R-2 の不変条件）。
func TestPlaceOrder_EventLogWrittenInSameTx(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	// (a) tx の手前で失敗するケース: 集約も配送キューもイベントログも空のまま。
	rolled := newMemFixture(t)
	rolled.reserver.EXPECT().
		Reserve(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(application.ErrReservationRejected)

	_, err := rolled.place.Handle(ctx, sampleInput())
	require.ErrorIs(t, err, application.ErrReservationRejected)
	assert.Empty(t, rolled.stores.Queued(), "配送キューは空のまま")
	assert.Empty(t, rolled.stores.Events(), "イベントログも空のまま（片方だけ残らない）")

	// (b) コミットするケース: 配送キューとイベントログの両方に同じメッセージが入る。
	ok := newMemFixture(t)
	ok.reserver.EXPECT().Reserve(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)

	id, err := ok.place.Handle(ctx, sampleInput())
	require.NoError(t, err, "注文作成")

	queued := ok.stores.Queued()
	recorded := ok.stores.Events()
	require.Len(t, queued, 1, "配送キューに 1 件")
	require.Len(t, recorded, 1, "イベントログに 1 件")
	assert.Equal(t, queued[0].ID, recorded[0].ID, "同じメッセージ ID")
	assert.Equal(t, queued[0].Type, recorded[0].Type, "同じ種別")
	assert.Equal(t, id.String(), decodeReservationRef(t, recorded[0].Payload), "同じペイロード")
}

func TestPlaceOrder_InsufficientStockRejected(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	f := newMemFixture(t)
	// 予約が業務的に拒否される（在庫不足＝在庫側 409 の翻訳）。補償解放は呼ばれない
	// （そもそも予約が成立していない）。Release への EXPECT を置かないことで、
	// gomock が「Release が呼ばれたら失敗」として検証する。
	f.reserver.EXPECT().
		Reserve(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(application.ErrReservationRejected)

	_, err := f.place.Handle(ctx, sampleInput())
	require.ErrorIs(t, err, application.ErrReservationRejected)
	// 注文は保存されず、コマンドも積まれていない（予約失敗は tx の前）。
	assert.Empty(t, f.stores.Queued())
}

func TestPlaceOrder_ReserveUnavailable(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	f := newMemFixture(t)
	// aclhttp が不達（timeout / 5xx）を翻訳したときの形（両番兵に一致）を再現する。
	f.reserver.EXPECT().
		Reserve(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(errors.Join(application.ErrReservationRejected, application.ErrReservationUnavailable))

	_, err := f.place.Handle(ctx, sampleInput())
	// HTTP マッパは Unavailable を先に判定して 503 を返す（[http_test.go] で検証）。
	require.ErrorIs(t, err, application.ErrReservationUnavailable)
	assert.Empty(t, f.stores.Queued())
}

func TestPlaceOrder_EmptyLinesRejected(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	f := newMemFixture(t)
	// ドメイン検証で失敗するため、在庫予約は呼ばれない。reserver に EXPECT を置かないので、
	// もし Reserve が呼ばれれば gomock がテストを失敗させる。

	_, err := f.place.Handle(ctx, application.PlaceOrderInput{CustomerID: "CUST-1"})
	require.ErrorIs(t, err, domain.ErrEmptyOrder)
}

func TestPlaceOrder_RetriesOnConflictReserveOnce(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	orderRows := memory.NewOrderRows()
	shipmentRows := memory.NewShipmentRows()
	stores := memory.NewStores()
	// 本物のインメモリ UoW を包み、最初の 1 回だけ ErrConcurrencyConflict を注入する。
	flaky := &flakyUoW{inner: memory.NewUnitOfWork(orderRows, shipmentRows, stores), failsLeft: 1}
	f := newMemFixtureWith(t, flaky, orderRows, shipmentRows, stores)

	// UoW は再試行されるが、ACL の予約は tx の外なのでちょうど 1 回だけ呼ばれる。
	f.reserver.EXPECT().
		Reserve(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil).
		Times(1)

	id, err := f.place.Handle(ctx, sampleInput())
	require.NoError(t, err)
	assert.Zero(t, flaky.failsLeft, "衝突注入が消費されているべき")

	// ロールバック分は破棄され、最終的に ConfirmReservation は 1 件だけ・version は 1。
	require.Len(t, filterByType(stores.Queued(), application.MessageTypeConfirmReservation), 1)
	// 恒久イベントログも同一コミットで確定するため、同じく 1 件だけ記録される。
	require.Len(t, filterByType(stores.Events(), application.MessageTypeConfirmReservation), 1)
	view, err := f.get.Handle(ctx, id.String())
	require.NoError(t, err)
	assert.Equal(t, 1, view.Version)
}
