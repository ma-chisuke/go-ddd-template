package application_test

// このファイルは application 層のテストで共有するハーネス（テストダブルと組み立てヘルパー）を
// 収める。team 方針「unit + integration（高速なドメインテスト向けのインメモリアダプタ ＋
// gomock のポートモック）」に沿って、2 通りの束を用意する。
//
//   - memFixture … 本物のインメモリアダプタ（Store / Stores / UnitOfWork）で永続化・
//     トランザクション・イベント配信まで通しで検証する束。外部同期呼び出しである在庫予約
//     （ACL ポート StockReserver）だけを gomock のモックへ差し替え、応答と呼び出し回数を厳密に縛る。
//   - mockPorts … OrderStore / MessagePublisher / Repos / EventDispatcher / StockReserver を
//     すべて gomock で差し替え、ポートの相互作用（呼び出し順・引数・回数）だけを検証する束
//     （[port_interaction_test.go] で使う）。

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/example/go-ddd-template/contexts/ordering/internal/adapter/outbound/memory"
	"github.com/example/go-ddd-template/contexts/ordering/internal/application"
	"github.com/example/go-ddd-template/contexts/ordering/internal/domain/order"
	"github.com/example/go-ddd-template/contexts/ordering/internal/mock"
	"github.com/example/go-ddd-template/shared/event"
	"github.com/example/go-ddd-template/shared/outbox"
	"github.com/example/go-ddd-template/shared/uow"
)

func testLogger() *slog.Logger { return slog.New(slog.NewJSONHandler(io.Discard, nil)) }

// newImmediateExecutor は再試行のバックオフを 0 にした Executor（テストを速く保つ）。
func newImmediateExecutor() uow.Executor { return uow.NewExecutor(uow.WithBaseBackoff(0)) }

// memFixture は本物のインメモリアダプタで注文ユースケース一式を組み立て、ACL ポートだけを
// gomock の StockReserver モックへ差し替えたテスト用の束。reserver に EXPECT() を設定してから
// 各ユースケースを呼ぶ。gomock.NewController(t) は t.Cleanup で自動 Finish されるため、
// 期待どおりに呼ばれたかの検証はテスト終了時に自動で走る。
type memFixture struct {
	place    *application.PlaceOrder
	get      *application.GetOrder
	cancel   *application.CancelOrder
	store    *memory.Store
	stores   *memory.Stores
	reserver *mock.MockStockReserver
	captured *[]order.DomainEvent
}

// newMemFixture はインメモリの UoW で束を組み立てる（最も一般的な構成）。
// 配送キュー（stores.Queued）と恒久イベントログ（stores.Events）は同一の Stores が束ね、
// 同一コミットで確定する。
func newMemFixture(t *testing.T) memFixture {
	t.Helper()
	store := memory.NewStore()
	stores := memory.NewStores()
	return newMemFixtureWith(t, memory.NewUnitOfWork(store, stores), store, stores)
}

// newMemFixtureWith は作業単位（UoW）を差し替えて束を組み立てる（衝突再試行の再現用）。
func newMemFixtureWith(t *testing.T, work application.UnitOfWork, store *memory.Store, stores *memory.Stores) memFixture {
	t.Helper()
	ctrl := gomock.NewController(t)
	reserver := mock.NewMockStockReserver(ctrl)
	exec := uow.NewExecutor(uow.WithBaseBackoff(0))
	log := testLogger()
	captured := &[]order.DomainEvent{}
	dispatcher := event.NewTyped[order.DomainEvent](log, func(_ context.Context, e order.DomainEvent) {
		*captured = append(*captured, e)
	})
	return memFixture{
		place:    application.NewPlaceOrder(exec, work, reserver, dispatcher, log),
		get:      application.NewGetOrder(memory.NewReadOrderStore(store), log),
		cancel:   application.NewCancelOrder(exec, work, log),
		store:    store,
		stores:   stores,
		reserver: reserver,
		captured: captured,
	}
}

// flakyUoW は最初の failsLeft 回だけ ErrConcurrencyConflict を注入する UoW デコレータ。
// 楽観的排他の再試行（uow.Run）を再現するために本物のインメモリ UoW を包む。
type flakyUoW struct {
	inner     application.UnitOfWork
	failsLeft int
}

func (f *flakyUoW) Within(ctx context.Context, fn func(ctx context.Context, r application.Repos) error) error {
	return f.inner.Within(ctx, func(ctx context.Context, r application.Repos) error {
		if f.failsLeft > 0 {
			f.failsLeft--
			return uow.ErrConcurrencyConflict
		}
		return fn(ctx, r)
	})
}

func sampleInput() application.PlaceOrderInput {
	return application.PlaceOrderInput{
		CustomerID: "CUST-1",
		Lines: []application.PlaceOrderLine{
			{SKU: "SKU-A", Quantity: 3, UnitPriceAmount: 1200, Currency: "JPY"},
		},
	}
}

func filterByType(msgs []outbox.Message, msgType string) []outbox.Message {
	var out []outbox.Message
	for _, m := range msgs {
		if m.Type == msgType {
			out = append(out, m)
		}
	}
	return out
}

// decodeReservationRef は在庫側の購読ポリシと同一の構造体で payload をデコードする。
// これにより「注文側が生む payload が、在庫側がデコードできる契約に一致する」ことを検証する。
func decodeReservationRef(t *testing.T, payload []byte) string {
	t.Helper()
	var p struct {
		ReservationRef string `json:"reservation_ref"`
	}
	require.NoError(t, json.Unmarshal(payload, &p), "payload のデコードに失敗しました")
	return p.ReservationRef
}

func countEvents(events []order.DomainEvent, name string) int {
	n := 0
	for _, e := range events {
		if e.EventName() == name {
			n++
		}
	}
	return n
}
