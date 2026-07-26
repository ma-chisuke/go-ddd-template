package application_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/example/go-ddd-template/contexts/inventory/internal/application"
	"github.com/example/go-ddd-template/contexts/inventory/internal/domain"
	"github.com/example/go-ddd-template/contexts/inventory/internal/mock"
	"github.com/example/go-ddd-template/shared/uow"
)

// これらのテストは「ユースケースがポートを正しく呼ぶか」という相互作用そのものを
// gomock の EXPECT で狙い撃ちで検証する。インメモリアダプタを使った統合テスト
// （replenish_test.go / reservation_flow_test.go / reaper_test.go）が振る舞いの
// 結線を確かめるのに対し、こちらは StockStore / MessagePublisher / Clock /
// EventDispatcher / Repos というポートの呼ばれ方を明示的に表明する。

// directUoW は与えられた Repos をそのままクロージャへ渡す、テスト用の最小の作業単位。
// gomock のポートモックを束ねた Repos をユースケースへ届けるために使う。擬似トランザクションや
// 再試行は伴わない（ポート相互作用の検証に集中するため）。application.UnitOfWork を満たす。
type directUoW struct {
	repos application.Repos
}

func (u directUoW) Within(ctx context.Context, fn func(ctx context.Context, repos application.Repos) error) error {
	return fn(ctx, u.repos)
}

// noRetryExec は再試行なし・バックオフなしの Executor（衝突注入はこの層で行わない）。
func noRetryExec() uow.Executor {
	return uow.NewExecutor(uow.WithBaseBackoff(0))
}

// mustSKU はテスト用に SKU を生成する。
func mustSKU(t *testing.T, s string) domain.SKU {
	t.Helper()
	sku, err := domain.NewSKU(s)
	require.NoError(t, err, "SKU の生成")
	return sku
}

// mustRef はテスト用に ReservationRef を生成する。
func mustRef(t *testing.T, s string) domain.ReservationRef {
	t.Helper()
	ref, err := domain.NewReservationRef(s)
	require.NoError(t, err, "ReservationRef の生成")
	return ref
}

// seededItem は available=n の在庫項目を作る（新規作成 → 補充 → 補充イベントは破棄）。
func seededItem(t *testing.T, sku string, n int) *domain.StockItem {
	t.Helper()
	item, err := domain.NewStockItem("id-"+sku, mustSKU(t, sku))
	require.NoError(t, err, "NewStockItem")
	require.NoError(t, item.Replenish(mustQuantity(t, n)), "Replenish")
	_ = item.PullEvents() // 補充イベントは以降のテストに無関係なので捨てる
	return item
}

// mustQuantity はテスト用に Quantity を生成する。
func mustQuantity(t *testing.T, n int) domain.Quantity {
	t.Helper()
	q, err := domain.NewQuantity(n)
	require.NoError(t, err, "Quantity の生成")
	return q
}

// reservedItem は ref で数量 qty を pending 予約した在庫項目を作る（Confirm / Release 用）。
// ttl 経過後に期限切れになるよう expiresAt が入る。
func reservedItem(t *testing.T, sku string, available, qty int, ref string, ttl time.Duration) *domain.StockItem {
	t.Helper()
	item := seededItem(t, sku, available)
	require.NoError(t, item.Reserve(mustRef(t, ref), mustQuantity(t, qty), ttl), "Reserve")
	_ = item.PullEvents()
	return item
}

func TestReplenish_NewSKU_LoadsThenSavesAndDispatches(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ctx := context.Background()

	stock := mock.NewMockStockStore(ctrl)
	repos := mock.NewMockRepos(ctrl)
	dispatch := mock.NewMockEventDispatcher(ctrl)

	repos.EXPECT().Stock().Return(stock).AnyTimes()

	// 未登録 SKU の補充: Load は NotFound → 新規作成 → Save で永続化（版 1 を反映）。
	stock.EXPECT().
		Load(gomock.Any(), gomock.Eq(mustSKU(t, "WIDGET-001"))).
		Return(nil, domain.ErrStockItemNotFound)
	stock.EXPECT().
		Save(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, items ...*domain.StockItem) error {
			require.Len(t, items, 1, "Save は 1 集約で呼ばれる")
			items[0].MarkPersisted(1) // 実アダプタが行う版反映を模す
			return nil
		})

	var dispatched []domain.DomainEvent
	dispatch.EXPECT().
		Dispatch(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, events ...domain.DomainEvent) {
			dispatched = append(dispatched, events...)
		})

	r := application.NewReplenisher(noRetryExec(), directUoW{repos}, dispatch, testLogger())
	res, err := r.Replenish(ctx, application.ReplenishInput{SKU: "WIDGET-001", Quantity: 10})

	require.NoError(t, err)
	assert.Equal(t, 10, res.Available)
	assert.Equal(t, 0, res.Reserved)
	assert.Equal(t, 1, res.Version)
	require.Len(t, dispatched, 1, "補充成功でイベントが 1 件配信される")
	assert.IsType(t, domain.StockReplenished{}, dispatched[0])
}

func TestReplenish_DoesNotWriteToOutbox(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ctx := context.Background()

	stock := mock.NewMockStockStore(ctrl)
	// MessagePublisher（アウトボックス）を結線するが、補充はプロセス内配信のみで
	// アウトボックスへは書かない。publisher に Enqueue の EXPECT を置かないことで、
	// strict な gomock がアウトボックスへの意図せぬ書き込みを検出する。
	publisher := mock.NewMockMessagePublisher(ctrl)
	repos := mock.NewMockRepos(ctrl)
	dispatch := mock.NewMockEventDispatcher(ctrl)

	repos.EXPECT().Stock().Return(stock).AnyTimes()
	repos.EXPECT().Outbox().Return(publisher).AnyTimes()

	stock.EXPECT().Load(gomock.Any(), gomock.Any()).Return(nil, domain.ErrStockItemNotFound)
	stock.EXPECT().Save(gomock.Any(), gomock.Any()).Return(nil)
	dispatch.EXPECT().Dispatch(gomock.Any(), gomock.Any())

	r := application.NewReplenisher(noRetryExec(), directUoW{repos}, dispatch, testLogger())
	_, err := r.Replenish(ctx, application.ReplenishInput{SKU: "WIDGET-001", Quantity: 5})
	require.NoError(t, err)
	// publisher.Enqueue が呼ばれていれば ctrl の自動 Finish がここで失敗させる。
}

func TestReserve_LoadsManyThenSaves(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ctx := context.Background()

	stock := mock.NewMockStockStore(ctrl)
	repos := mock.NewMockRepos(ctrl)
	dispatch := mock.NewMockEventDispatcher(ctrl)

	repos.EXPECT().Stock().Return(stock).AnyTimes()

	want := []domain.SKU{mustSKU(t, "SKU-A")}
	stock.EXPECT().
		LoadMany(gomock.Any(), gomock.Eq(want)).
		Return([]*domain.StockItem{seededItem(t, "SKU-A", 10)}, nil)
	stock.EXPECT().
		Save(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, items ...*domain.StockItem) error {
			require.Len(t, items, 1)
			assert.Equal(t, 6, items[0].Available().Int(), "予約後 available = 10 - 4")
			assert.Equal(t, 4, items[0].Reserved().Int())
			return nil
		})

	var dispatched []domain.DomainEvent
	dispatch.EXPECT().
		Dispatch(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, events ...domain.DomainEvent) {
			dispatched = append(dispatched, events...)
		})

	r := application.NewReserver(noRetryExec(), directUoW{repos}, dispatch, testLogger(), time.Hour)
	err := r.Reserve(ctx, application.ReserveInput{
		Ref:   "ORDER-1",
		Lines: []application.ReserveLine{{SKU: "SKU-A", Quantity: 4}},
	})
	require.NoError(t, err)
	require.NotEmpty(t, dispatched)
	assert.Equal(t, "inventory.stock_reserved", dispatched[0].EventName())
}

func TestConfirm_LoadsByReservationThenSaves(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ctx := context.Background()

	stock := mock.NewMockStockStore(ctrl)
	repos := mock.NewMockRepos(ctrl)
	dispatch := mock.NewMockEventDispatcher(ctrl)

	repos.EXPECT().Stock().Return(stock).AnyTimes()

	ref := mustRef(t, "ORDER-1")
	stock.EXPECT().
		LoadByReservation(gomock.Any(), gomock.Eq(ref)).
		Return([]*domain.StockItem{reservedItem(t, "SKU-A", 10, 4, "ORDER-1", time.Hour)}, nil)
	stock.EXPECT().Save(gomock.Any(), gomock.Any()).Return(nil)

	var dispatched []domain.DomainEvent
	dispatch.EXPECT().
		Dispatch(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, events ...domain.DomainEvent) {
			dispatched = append(dispatched, events...)
		})

	c := application.NewConfirmer(noRetryExec(), directUoW{repos}, dispatch, testLogger())
	require.NoError(t, c.Confirm(ctx, "ORDER-1"))
	require.NotEmpty(t, dispatched)
	assert.Equal(t, "inventory.stock_reservation_confirmed", dispatched[0].EventName())
}

func TestConfirm_NoReservation_ReturnsNotFound(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ctx := context.Background()

	stock := mock.NewMockStockStore(ctrl)
	repos := mock.NewMockRepos(ctrl)
	dispatch := mock.NewMockEventDispatcher(ctrl)

	repos.EXPECT().Stock().Return(stock).AnyTimes()
	// 有効な予約が皆無 → ErrReservationNotFound。Save も Dispatch も呼ばれない
	// （EXPECT を置かないことで strict gomock が誤呼び出しを検出する）。
	stock.EXPECT().
		LoadByReservation(gomock.Any(), gomock.Eq(mustRef(t, "UNKNOWN"))).
		Return(nil, nil)

	c := application.NewConfirmer(noRetryExec(), directUoW{repos}, dispatch, testLogger())
	err := c.Confirm(ctx, "UNKNOWN")
	require.ErrorIs(t, err, domain.ErrReservationNotFound)
}

func TestRelease_LoadsByReservationThenSaves(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ctx := context.Background()

	stock := mock.NewMockStockStore(ctrl)
	repos := mock.NewMockRepos(ctrl)
	dispatch := mock.NewMockEventDispatcher(ctrl)

	repos.EXPECT().Stock().Return(stock).AnyTimes()

	ref := mustRef(t, "ORDER-1")
	stock.EXPECT().
		LoadByReservation(gomock.Any(), gomock.Eq(ref)).
		Return([]*domain.StockItem{reservedItem(t, "SKU-A", 10, 4, "ORDER-1", time.Hour)}, nil)
	stock.EXPECT().
		Save(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, items ...*domain.StockItem) error {
			require.Len(t, items, 1)
			assert.Equal(t, 10, items[0].Available().Int(), "解放後は available へ戻る")
			assert.Equal(t, 0, items[0].Reserved().Int())
			return nil
		})

	var dispatched []domain.DomainEvent
	dispatch.EXPECT().
		Dispatch(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, events ...domain.DomainEvent) {
			dispatched = append(dispatched, events...)
		})

	r := application.NewReleaser(noRetryExec(), directUoW{repos}, dispatch, testLogger())
	require.NoError(t, r.Release(ctx, "ORDER-1"))
	require.NotEmpty(t, dispatched)
	assert.Equal(t, "inventory.stock_released", dispatched[0].EventName())
}

func TestQueryStock_ReadsViaStockStore(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ctx := context.Background()

	// 照会は作業単位を使わず、読み取り用 StockStore に直接ぶら下がる。
	read := mock.NewMockStockStore(ctrl)
	read.EXPECT().
		Load(gomock.Any(), gomock.Eq(mustSKU(t, "WIDGET-001"))).
		Return(reservedItem(t, "WIDGET-001", 10, 3, "R-1", time.Hour), nil)

	v := application.NewStockViewer(read, testLogger())
	res, err := v.QueryStock(ctx, application.QueryStockInput{SKU: "WIDGET-001"})
	require.NoError(t, err)
	assert.Equal(t, "WIDGET-001", res.SKU)
	assert.Equal(t, 7, res.Available, "available = 10 - 3(予約)")
	assert.Equal(t, 3, res.Reserved)
}

func TestReaper_UsesClockThenLoadsExpiredAndSaves(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	ctx := context.Background()

	stock := mock.NewMockStockStore(ctrl)
	repos := mock.NewMockRepos(ctrl)
	dispatch := mock.NewMockEventDispatcher(ctrl)
	clock := mock.NewMockClock(ctrl)

	repos.EXPECT().Stock().Return(stock).AnyTimes()

	// 擬似時計は実時刻 + 2 時間を返す → TTL 1 時間の pending は期限切れ。
	now := time.Now().Add(2 * time.Hour)
	clock.EXPECT().Now().Return(now)

	// Reaper は Clock.Now() の時刻とバッチ上限で LoadExpiredPending を呼ぶ。
	expired := reservedItem(t, "SKU-A", 100, 10, "PENDING", time.Hour)
	stock.EXPECT().
		LoadExpiredPending(gomock.Any(), gomock.Eq(now), gomock.Eq(100)).
		Return([]*domain.StockItem{expired}, nil)
	stock.EXPECT().Save(gomock.Any(), gomock.Any()).Return(nil)

	var dispatched []domain.DomainEvent
	dispatch.EXPECT().
		Dispatch(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, events ...domain.DomainEvent) {
			dispatched = append(dispatched, events...)
		})

	reaper := application.NewReaper(noRetryExec(), directUoW{repos}, dispatch, clock, testLogger(), 100)
	require.NoError(t, reaper.Sweep(ctx))
	require.NotEmpty(t, dispatched, "期限切れ pending の解放イベントが配信される")
	assert.Equal(t, "inventory.stock_released", dispatched[0].EventName())
}
