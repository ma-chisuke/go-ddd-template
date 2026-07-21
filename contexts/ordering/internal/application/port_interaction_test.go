package application_test

// このファイルは、注文ユースケースを「すべて gomock のポートモックで駆動」して、ポートの
// 相互作用（呼び出し順・引数・回数・ルーティング）だけを厳密に検証するテスト群。永続化の
// 実挙動（version 増分など）は本物のインメモリアダプタで別途検証しており（[place_order_test.go]
// / [cancel_order_test.go]）、ここでは Save / Enqueue / Dispatch がどのポートへどう流れるかに集中する。

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/example/go-ddd-template/contexts/ordering/internal/application"
	"github.com/example/go-ddd-template/contexts/ordering/internal/domain/order"
	"github.com/example/go-ddd-template/contexts/ordering/internal/mock"
	"github.com/example/go-ddd-template/shared/outbox"
)

// mockPorts は application 層の全ポートを gomock で差し替えたモック一式。
type mockPorts struct {
	reserver *mock.MockStockReserver
	orders   *mock.MockOrderStore
	outbox   *mock.MockMessagePublisher
	repos    *mock.MockRepos
	dispatch *mock.MockEventDispatcher
}

// newMockPorts は全ポートのモックを生成し、Repos が tx に束ねた OrderStore / MessagePublisher を
// 返すよう配線する（Repos もポートなので gomock で実装する）。
func newMockPorts(t *testing.T) mockPorts {
	t.Helper()
	ctrl := gomock.NewController(t)
	orders := mock.NewMockOrderStore(ctrl)
	publisher := mock.NewMockMessagePublisher(ctrl)
	repos := mock.NewMockRepos(ctrl)
	repos.EXPECT().Orders().Return(orders).AnyTimes()
	repos.EXPECT().Outbox().Return(publisher).AnyTimes()
	return mockPorts{
		reserver: mock.NewMockStockReserver(ctrl),
		orders:   orders,
		outbox:   publisher,
		repos:    repos,
		dispatch: mock.NewMockEventDispatcher(ctrl),
	}
}

// stubUoW は mock の Repos を closure にそのまま渡すトランザクション境界のテストダブル。
// 実トランザクションは張らず、UoW の内側で使われるポート（OrderStore / MessagePublisher）への
// 呼び出しを gomock の EXPECT で厳密に検証するために使う。
type stubUoW struct{ repos application.Repos }

func (u stubUoW) Within(ctx context.Context, fn func(ctx context.Context, r application.Repos) error) error {
	return fn(ctx, u.repos)
}

func (m mockPorts) placeUseCase() *application.PlaceOrder {
	return application.NewPlaceOrder(newImmediateExecutor(), stubUoW{repos: m.repos}, m.reserver, m.dispatch, testLogger())
}

func (m mockPorts) cancelUseCase() *application.CancelOrder {
	return application.NewCancelOrder(newImmediateExecutor(), stubUoW{repos: m.repos}, testLogger())
}

// eventNamed は指定名のドメインイベントに一致する gomock マッチャ（Dispatch のルーティング検証用）。
func eventNamed(name string) gomock.Matcher {
	return eventNameMatcher{name: name}
}

type eventNameMatcher struct{ name string }

func (m eventNameMatcher) Matches(x any) bool {
	e, ok := x.(order.DomainEvent)
	return ok && e.EventName() == m.name
}
func (m eventNameMatcher) String() string { return "DomainEvent(" + m.name + ")" }

// TestPlaceOrder_PortInteractions は、注文作成が Reserve → Save → Enqueue(ConfirmReservation)
// → Dispatch(OrderPlaced) の順に各ポートへ正しく流れることを、すべてモックの EXPECT で検証する。
func TestPlaceOrder_PortInteractions(t *testing.T) {
	m := newMockPorts(t)

	var enqueued outbox.Message
	gomock.InOrder(
		m.reserver.EXPECT().Reserve(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil),
		m.orders.EXPECT().Save(gomock.Any(), gomock.Any()).Return(nil),
		m.outbox.EXPECT().Enqueue(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, msg outbox.Message) error {
				enqueued = msg
				return nil
			}),
	)
	// コミット後、OrderPlaced がプロセス内ディスパッチャへ届く。
	m.dispatch.EXPECT().Dispatch(gomock.Any(), eventNamed("ordering.order_placed"))

	id, err := m.placeUseCase().Handle(context.Background(), sampleInput())
	require.NoError(t, err)
	assert.False(t, id.IsZero())
	// アウトボックスへは ConfirmReservation コマンドが、注文 ID を運ぶ payload で積まれる。
	assert.Equal(t, application.MessageTypeConfirmReservation, enqueued.Type)
	assert.Equal(t, id.String(), decodeReservationRef(t, enqueued.Payload))
}

// TestPlaceOrder_SaveFailureReleasesCompensating は、保存（フェーズ 2）が失敗したとき、
// 予約成立済みなので best-effort な補償解放（Release）がちょうど 1 回呼ばれることを検証する。
func TestPlaceOrder_SaveFailureReleasesCompensating(t *testing.T) {
	m := newMockPorts(t)

	m.reserver.EXPECT().Reserve(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	m.orders.EXPECT().Save(gomock.Any(), gomock.Any()).Return(errors.New("DB 書き込み失敗"))
	// 保存失敗後に補償解放が呼ばれる。Enqueue は到達しない（EXPECT を置かない＝0 回）。
	m.reserver.EXPECT().Release(gomock.Any(), gomock.Any()).Return(nil).Times(1)

	_, err := m.placeUseCase().Handle(context.Background(), sampleInput())
	require.Error(t, err)
}

// TestPlaceOrder_SaveFailureReleaseAlsoFails は、補償解放も失敗する場合でも、解放の失敗は
// ログに留め、呼び出し元へは元の保存失敗を返すことを検証する。
func TestPlaceOrder_SaveFailureReleaseAlsoFails(t *testing.T) {
	m := newMockPorts(t)

	saveErr := errors.New("DB 書き込み失敗")
	m.reserver.EXPECT().Reserve(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	m.orders.EXPECT().Save(gomock.Any(), gomock.Any()).Return(saveErr)
	m.reserver.EXPECT().Release(gomock.Any(), gomock.Any()).Return(errors.New("解放も失敗")).Times(1)

	_, err := m.placeUseCase().Handle(context.Background(), sampleInput())
	require.ErrorIs(t, err, saveErr)
}

// TestCancelOrder_RoutesOrderCancelledToOutbox は、取消が Load → Save → Enqueue(OrderCancelled)
// の順に各ポートへ流れ、翻訳済み payload（reservation_ref）を運ぶことを検証する。
func TestCancelOrder_RoutesOrderCancelledToOutbox(t *testing.T) {
	m := newMockPorts(t)
	o := newConfirmedOrder(t, "ORDER-9")

	var enqueued outbox.Message
	gomock.InOrder(
		m.orders.EXPECT().Load(gomock.Any(), gomock.Any()).Return(o, nil),
		m.orders.EXPECT().Save(gomock.Any(), gomock.Any()).Return(nil),
		m.outbox.EXPECT().Enqueue(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, msg outbox.Message) error {
				enqueued = msg
				return nil
			}),
	)

	require.NoError(t, m.cancelUseCase().Handle(context.Background(), "ORDER-9"))
	assert.Equal(t, application.MessageTypeOrderCancelled, enqueued.Type)
	assert.Equal(t, o.ReservationRef().String(), decodeReservationRef(t, enqueued.Payload))
}

// TestCancelOrder_LoadNotFoundPropagates は、取消対象の読み込みで見つからないとき、その番兵を
// そのまま伝播する（Save / Enqueue は呼ばれない）ことを検証する。
func TestCancelOrder_LoadNotFoundPropagates(t *testing.T) {
	m := newMockPorts(t)
	m.orders.EXPECT().Load(gomock.Any(), gomock.Any()).Return(nil, order.ErrOrderNotFound)

	err := m.cancelUseCase().Handle(context.Background(), "ORDER-9")
	require.ErrorIs(t, err, order.ErrOrderNotFound)
}

// TestGetOrder_LoadErrorPropagates は、照会が読み取り用 OrderStore（モック）の Load エラーを
// そのまま伝播することを検証する。
func TestGetOrder_LoadErrorPropagates(t *testing.T) {
	m := newMockPorts(t)
	m.orders.EXPECT().Load(gomock.Any(), gomock.Any()).Return(nil, order.ErrOrderNotFound)

	get := application.NewGetOrder(m.orders, testLogger())
	_, err := get.Handle(context.Background(), "ORDER-9")
	require.ErrorIs(t, err, order.ErrOrderNotFound)
}

// newConfirmedOrder は Confirmed・version 1（永続化済み相当）の注文集約を組み立てる。Load が
// 返す既存注文の代役として使う（イベントは復元相当のため未保持にする）。
func newConfirmedOrder(t *testing.T, idStr string) *order.Order {
	t.Helper()
	oid, err := order.NewOrderID(idStr)
	require.NoError(t, err)
	cust, err := order.NewCustomerID("CUST-1")
	require.NoError(t, err)
	sku, err := order.NewSKU("SKU-A")
	require.NoError(t, err)
	qty, err := order.NewQuantity(2)
	require.NoError(t, err)
	price, err := order.NewMoney(1000, "JPY")
	require.NoError(t, err)
	o, err := order.NewOrder(oid, cust, []order.OrderLine{order.NewOrderLine(sku, qty, price)})
	require.NoError(t, err)
	o.PullEvents()     // OrderPlaced を捨てる（Load は復元相当でイベントを持たない）
	o.MarkPersisted(1) // 永続化済み version=1 を模す
	return o
}
