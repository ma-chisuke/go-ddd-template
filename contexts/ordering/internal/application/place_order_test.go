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
	"github.com/example/go-ddd-template/contexts/ordering/internal/domain/order"
	"github.com/example/go-ddd-template/contexts/ordering/port"
)

// これらは本物のインメモリアダプタ（永続化・トランザクション・イベント配信）で通しに検証しつつ、
// 外部同期呼び出しの在庫予約（ACL ポート）だけを gomock の StockReserver モックへ差し替える
// テスト群。予約の応答と呼び出し回数はモックの EXPECT で厳密に縛る。純粋なポート相互作用
// （Save/Enqueue のルーティング）だけを見るテストは [port_interaction_test.go] にある。

func TestPlaceOrder_Happy(t *testing.T) {
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
	confirms := filterByType(f.obx.Messages(), application.MessageTypeConfirmReservation)
	require.Len(t, confirms, 1)
	assert.Equal(t, id.String(), decodeReservationRef(t, confirms[0].Payload))

	// OrderPlaced はコミット後にプロセス内配信されている。
	assert.Equal(t, 1, countEvents(*f.captured, "ordering.order_placed"))
}

func TestPlaceOrder_InsufficientStockRejected(t *testing.T) {
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
	assert.Empty(t, f.obx.Messages())
}

func TestPlaceOrder_ReserveUnavailable(t *testing.T) {
	ctx := context.Background()
	f := newMemFixture(t)
	// aclhttp が不達（timeout / 5xx）を翻訳したときの形（両番兵に一致）を再現する。
	f.reserver.EXPECT().
		Reserve(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(errors.Join(application.ErrReservationRejected, application.ErrReservationUnavailable))

	_, err := f.place.Handle(ctx, sampleInput())
	// HTTP マッパは Unavailable を先に判定して 503 を返す（[http_test.go] で検証）。
	require.ErrorIs(t, err, application.ErrReservationUnavailable)
	assert.Empty(t, f.obx.Messages())
}

func TestPlaceOrder_EmptyLinesRejected(t *testing.T) {
	ctx := context.Background()
	f := newMemFixture(t)
	// ドメイン検証で失敗するため、在庫予約は呼ばれない。reserver に EXPECT を置かないので、
	// もし Reserve が呼ばれれば gomock がテストを失敗させる。

	_, err := f.place.Handle(ctx, application.PlaceOrderInput{CustomerID: "CUST-1"})
	require.ErrorIs(t, err, order.ErrEmptyOrder)
}

func TestPlaceOrder_RetriesOnConflictReserveOnce(t *testing.T) {
	ctx := context.Background()
	store := memory.NewStore()
	obx := memory.NewOutboxStore()
	// 本物のインメモリ UoW を包み、最初の 1 回だけ ErrConcurrencyConflict を注入する。
	flaky := &flakyUoW{inner: memory.NewUnitOfWork(store, obx), failsLeft: 1}
	f := newMemFixtureWith(t, flaky, store, obx)

	// UoW は再試行されるが、ACL の予約は tx の外なのでちょうど 1 回だけ呼ばれる。
	f.reserver.EXPECT().
		Reserve(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil).
		Times(1)

	id, err := f.place.Handle(ctx, sampleInput())
	require.NoError(t, err)
	assert.Zero(t, flaky.failsLeft, "衝突注入が消費されているべき")

	// ロールバック分は破棄され、最終的に ConfirmReservation は 1 件だけ・version は 1。
	require.Len(t, filterByType(obx.Messages(), application.MessageTypeConfirmReservation), 1)
	view, err := f.get.Handle(ctx, id.String())
	require.NoError(t, err)
	assert.Equal(t, 1, view.Version)
}
