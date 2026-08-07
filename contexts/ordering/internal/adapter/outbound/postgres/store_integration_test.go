//go:build integration

// このファイルは build タグ `integration` を付けたときだけコンパイル・実行される。
// 通常の `go test ./...` では対象外なので、DB が無くてもテストは失敗しない。
// 実行するには、稼働中の PostgreSQL を指す DATABASE_URL を設定して
//
//	go test -tags=integration ./...
//
// のように実行する（docker compose up で立ち上がる DB を利用できる）。
package postgres_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/example/go-ddd-template/contexts/ordering/internal/adapter/outbound/postgres"
	"github.com/example/go-ddd-template/contexts/ordering/internal/application"
	"github.com/example/go-ddd-template/contexts/ordering/internal/domain"
	"github.com/example/go-ddd-template/contexts/ordering/port"
	"github.com/example/go-ddd-template/shared/event"
	"github.com/example/go-ddd-template/shared/outbox"
	"github.com/example/go-ddd-template/shared/uow"
)

func testLogger() *slog.Logger { return slog.New(slog.NewJSONHandler(io.Discard, nil)) }

// okReserver は予約が常に成功する StockReserver（DB 経路の検証に集中するため）。
type okReserver struct{}

func (okReserver) Reserve(_ context.Context, _ string, _ []port.ReserveLine) error { return nil }
func (okReserver) Release(_ context.Context, _ string) error                       { return nil }

// setupPool は DATABASE_URL からプールを作り、テーブルを空にして返す。
func setupPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL が未設定のためスキップします")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err, "プール生成に失敗")
	t.Cleanup(pool.Close)

	// order_lines は orders を参照するため CASCADE で一括初期化する。
	// events は恒久ログだがテストの再実行で ID が衝突するため、ここでは併せて初期化する。
	// shipments は orders への FK を持たない（集約間の整合性はアプリケーションが担う）ので
	// CASCADE の対象にならず、明示的に並べる必要がある。
	_, err = pool.Exec(ctx, "TRUNCATE ordering.orders, ordering.shipments, ordering.outbox, ordering.events CASCADE")
	require.NoError(t, err, "テーブル初期化に失敗（スキーマ未適用の可能性）")
	return pool
}

// countRows は指定テーブルの行数を返す（events 表の検証用）。
func countRows(t *testing.T, pool *pgxpool.Pool, table string) int {
	t.Helper()
	var n int
	err := pool.QueryRow(context.Background(), "SELECT count(*) FROM "+table).Scan(&n)
	require.NoError(t, err, "行数の取得に失敗")
	return n
}

// logCounts は観測した行数をテスト出力へ残す（-v で読める）。
// 決定的アサーション対の「実際に何行だったか」は、assert が通ったときには表に出ない。
// 受け入れ条件の検証として観測値そのものを記録に残すために出しておく。
func logCounts(t *testing.T, pool *pgxpool.Pool, phase string, tables ...string) {
	t.Helper()
	for _, table := range tables {
		t.Logf("%s: %s = %d 行", phase, table, countRows(t, pool, table))
	}
}

// pgFixture は postgres アダプタで注文ユースケース一式を組み立てる。
type pgFixture struct {
	place       *application.PlaceOrder
	get         *application.GetOrder
	cancel      *application.CancelOrder
	prepareShip *application.PrepareShipment
	markShipped *application.MarkShipped
	getShip     *application.GetShipment
	work        *postgres.UnitOfWork
	pool        *pgxpool.Pool
}

func newPgFixture(t *testing.T, pool *pgxpool.Pool) pgFixture {
	t.Helper()
	return newPgFixtureWith(t, pool, postgres.NewUnitOfWork(pool))
}

// newPgFixtureWith は作業単位を差し替えて束を組み立てる（ロールバックの再現用）。
// 差し替えても集約ストアの実装（postgres）は同じで、変わるのはトランザクションの結末だけである。
func newPgFixtureWith(t *testing.T, pool *pgxpool.Pool, work application.UnitOfWork) pgFixture {
	t.Helper()
	log := testLogger()
	exec := uow.NewExecutor()
	dispatcher := event.NewTyped[domain.DomainEvent](log)
	readOrders := postgres.NewReadOrderStore(pool)
	return pgFixture{
		place:       application.NewPlaceOrder(exec, work, okReserver{}, dispatcher, log),
		get:         application.NewGetOrder(readOrders, log),
		cancel:      application.NewCancelOrder(exec, work, log),
		prepareShip: application.NewPrepareShipment(exec, work, readOrders, dispatcher, log),
		markShipped: application.NewMarkShipped(exec, work, dispatcher, log),
		getShip:     application.NewGetShipment(postgres.NewReadShipmentStore(pool), log),
		work:        postgres.NewUnitOfWork(pool),
		pool:        pool,
	}
}

// errAborted はコミット直前にトランザクションを中断させる番兵（ロールバックの再現用）。
var errAborted = errors.New("検証のため中断")

// abortingUoW は fn の実行後に必ずエラーを返す UoW デコレータ。
//
// **書き込みは実際に行われたうえでトランザクションが中断する**ので、「そもそも書かなかった」
// のではなく「書いたがロールバックされた」ことを検証できる。衝突（ErrConcurrencyConflict）
// ではないので uow.Run は再試行せず即座に返す。
type abortingUoW struct{ inner application.UnitOfWork }

func (a abortingUoW) Within(ctx context.Context, fn func(ctx context.Context, r application.Repos) error) error {
	return a.inner.Within(ctx, func(ctx context.Context, r application.Repos) error {
		if err := fn(ctx, r); err != nil {
			return err
		}
		return errAborted
	})
}

func sampleInput() application.PlaceOrderInput {
	return application.PlaceOrderInput{
		CustomerID: "CUST-1",
		Lines: []application.PlaceOrderLine{
			{SKU: "SKU-A", Quantity: 3, UnitPriceAmount: 1200, Currency: "JPY"},
			{SKU: "SKU-B", Quantity: 1, UnitPriceAmount: 500, Currency: "JPY"},
		},
	}
}

func TestPostgres_PlaceThenGet(t *testing.T) {
	ctx := context.Background()
	pool := setupPool(t)
	f := newPgFixture(t, pool)

	id, err := f.place.Handle(ctx, sampleInput())
	require.NoError(t, err, "Place 失敗")
	view, err := f.get.Handle(ctx, id.String())
	require.NoError(t, err, "Get 失敗")
	assert.Equal(t, "confirmed", view.Status)
	assert.Equal(t, 1, view.Version)
	assert.Equal(t, int64(4100), view.TotalAmount)
	assert.Len(t, view.Lines, 2)

	// ConfirmReservation コマンドが outbox に積まれている。
	store := postgres.NewOutboxStore(pool)
	msgs, err := store.Unpublished(ctx, 10)
	require.NoError(t, err, "Unpublished 失敗")
	require.Len(t, msgs, 1)
	assert.Equal(t, application.MessageTypeConfirmReservation, msgs[0].Type)

	// 同一トランザクションで events 表にも記録されている。
	assert.Equal(t, 1, countRows(t, pool, "ordering.events"), "イベントログの行数")
}

func TestPostgres_CancelEnqueuesOrderCancelled(t *testing.T) {
	ctx := context.Background()
	pool := setupPool(t)
	f := newPgFixture(t, pool)

	id, err := f.place.Handle(ctx, sampleInput())
	require.NoError(t, err, "Place 失敗")
	require.NoError(t, f.cancel.Handle(ctx, id.String()), "Cancel 失敗")
	view, _ := f.get.Handle(ctx, id.String())
	assert.Equal(t, "cancelled", view.Status)
	assert.Equal(t, 2, view.Version)

	// ConfirmReservation と OrderCancelled の 2 件が積まれている。
	store := postgres.NewOutboxStore(pool)
	msgs, _ := store.Unpublished(ctx, 10)
	var hasCancelled bool
	for _, m := range msgs {
		if m.Type == application.MessageTypeOrderCancelled {
			hasCancelled = true
		}
	}
	assert.True(t, hasCancelled, "OrderCancelled が積まれていない")
}

func TestPostgres_OutboxRelay(t *testing.T) {
	ctx := context.Background()
	pool := setupPool(t)
	f := newPgFixture(t, pool)

	_, err := f.place.Handle(ctx, sampleInput())
	require.NoError(t, err, "Place 失敗")

	store := postgres.NewOutboxStore(pool)
	pub := &countingPublisher{}
	runner := outbox.NewRunner(store, pub, testLogger(), outbox.WithBatch(10))
	sent, err := runner.RunOnce(ctx)
	require.NoError(t, err, "RunOnce 失敗")
	assert.Equal(t, 1, sent)
	assert.Equal(t, 1, pub.count)

	// 配送後、outbox の行は消え、events の記録は残る（配送キューと恒久ログの分離）。
	assert.Zero(t, countRows(t, pool, "ordering.outbox"), "配送後の配送キューは空")
	assert.Equal(t, 1, countRows(t, pool, "ordering.events"), "配送後もイベントログは残る")

	// 2 回目は配送キューが空なので 0 件。
	sent, err = runner.RunOnce(ctx)
	require.NoError(t, err, "2 回目 RunOnce 失敗")
	assert.Zero(t, sent, "2 回目の送出件数")
}

// TestPostgres_EventLogRollsBackWithAggregate は、UoW がロールバックすると
// 集約・配送キュー・イベントログの 3 者すべてが巻き戻ることを実 DB で確認する
// （events が同一トランザクションで書かれている証拠）。
func TestPostgres_EventLogRollsBackWithAggregate(t *testing.T) {
	ctx := context.Background()
	pool := setupPool(t)
	f := newPgFixture(t, pool)

	// まず 1 件作って、確定済みの状態を作る。
	id, err := f.place.Handle(ctx, sampleInput())
	require.NoError(t, err, "Place 失敗")
	before := countRows(t, pool, "ordering.events")
	require.Equal(t, 1, before, "初期のイベントログ件数")

	// 取消を積んだうえで中断すると、集約の版も outbox も events も変化しない。
	loaded := loadVia(t, postgres.NewReadOrderStore(pool), id)
	sentinel := errors.New("業務都合で中断")
	err = f.work.Within(ctx, func(ctx context.Context, r application.Repos) error {
		_ = loaded.Cancel()
		if err := r.Orders().Save(ctx, loaded); err != nil {
			return err
		}
		if err := r.Outbox().Enqueue(ctx, outbox.Message{
			ID:         "rollback-1",
			Type:       application.MessageTypeOrderCancelled,
			Payload:    []byte(`{}`),
			OccurredAt: time.Now().UTC(),
		}); err != nil {
			return err
		}
		return sentinel // ロールバック
	})
	require.ErrorIs(t, err, sentinel)

	assert.Equal(t, before, countRows(t, pool, "ordering.events"), "ロールバック時はイベントログが増えない")
	assert.Equal(t, 1, countRows(t, pool, "ordering.outbox"), "ロールバック時は配送キューも増えない")
	view, err := f.get.Handle(ctx, id.String())
	require.NoError(t, err, "Get 失敗")
	assert.Equal(t, "confirmed", view.Status, "集約も巻き戻っている")
	assert.Equal(t, 1, view.Version, "版も進んでいない")
}

func TestPostgres_ConcurrencyConflict(t *testing.T) {
	ctx := context.Background()
	pool := setupPool(t)
	f := newPgFixture(t, pool)
	read := postgres.NewReadOrderStore(pool)

	id, err := f.place.Handle(ctx, sampleInput())
	require.NoError(t, err, "Place 失敗")

	// 同一注文の 2 つの版 1 コピーを読み出す。
	stale := loadVia(t, read, id) // version 1
	other := loadVia(t, read, id) // version 1

	// 先に other を取消して保存（version 2 になる）。
	_ = other.Cancel()
	err = f.work.Within(ctx, func(ctx context.Context, r application.Repos) error {
		return r.Orders().Save(ctx, other)
	})
	require.NoError(t, err, "other の保存 失敗")

	// stale（version 1）を取消して保存すると版が食い違い衝突する。
	_ = stale.Cancel()
	err = f.work.Within(ctx, func(ctx context.Context, r application.Repos) error {
		return r.Orders().Save(ctx, stale)
	})
	require.ErrorIs(t, err, uow.ErrConcurrencyConflict)
}

// countingPublisher は送出回数を数えるだけの Publisher。
type countingPublisher struct{ count int }

func (p *countingPublisher) Publish(_ context.Context, _ outbox.Message) error {
	p.count++
	return nil
}

// loadVia は読み取りストア経由で注文を読み込む。
func loadVia(t *testing.T, read application.OrderStore, id domain.OrderID) *domain.Order {
	t.Helper()
	o, err := read.Load(context.Background(), id)
	require.NoError(t, err, "読み込み 失敗")
	return o
}

// TestPostgres_PlaceOrderIsAtomicAcrossThreeTables は、staging 機構を汎化したあとも
// PlaceOrder の原子性が保たれていることを実 DB で確認する。
//
// **負側と正側を 1 回の観測で対にする。** ロールバック時に orders / outbox / events が
// **すべて 0 行**、コミット時に**すべて 1 行**。片側だけなら「何も書かない実装」も
// 「ロールバックを無視する実装」も通過してしまう。3 表を並べるのは、集約・配送キュー・
// 恒久ログが同一トランザクションに載っていることがこの経路の要だからである。
func TestPostgres_PlaceOrderIsAtomicAcrossThreeTables(t *testing.T) {
	ctx := context.Background()
	pool := setupPool(t)

	// 負側: 実際に書いたうえで中断すると、3 表とも 0 行のまま。
	aborting := newPgFixtureWith(t, pool, abortingUoW{inner: postgres.NewUnitOfWork(pool)})
	_, err := aborting.place.Handle(ctx, sampleInput())
	require.ErrorIs(t, err, errAborted, "中断が伝播する")
	logCounts(t, pool, "ロールバック後", "ordering.orders", "ordering.outbox", "ordering.events")
	assert.Zero(t, countRows(t, pool, "ordering.orders"), "ロールバック後の orders")
	assert.Zero(t, countRows(t, pool, "ordering.outbox"), "ロールバック後の outbox")
	assert.Zero(t, countRows(t, pool, "ordering.events"), "ロールバック後の events")

	// 正側: 同じ操作をコミットすると、3 表とも 1 行になる。
	f := newPgFixture(t, pool)
	_, err = f.place.Handle(ctx, sampleInput())
	require.NoError(t, err, "Place 失敗")
	logCounts(t, pool, "コミット後", "ordering.orders", "ordering.outbox", "ordering.events")
	assert.Equal(t, 1, countRows(t, pool, "ordering.orders"), "コミット後の orders")
	assert.Equal(t, 1, countRows(t, pool, "ordering.outbox"), "コミット後の outbox")
	assert.Equal(t, 1, countRows(t, pool, "ordering.events"), "コミット後の events")
}

// TestPostgres_ShipmentRollbackAndCommit は、2 つ目の集約ルートを足したあとも
// 実トランザクションの意味論が変わっていないことを確認する（AC-13(c)）。
func TestPostgres_ShipmentRollbackAndCommit(t *testing.T) {
	ctx := context.Background()
	pool := setupPool(t)
	f := newPgFixture(t, pool)

	id, err := f.place.Handle(ctx, sampleInput())
	require.NoError(t, err, "前準備の注文作成に失敗")
	orderID := id.String()

	// 負側: 出荷を書いたうえで中断すると 0 行のまま。
	aborting := newPgFixtureWith(t, pool, abortingUoW{inner: postgres.NewUnitOfWork(pool)})
	_, err = aborting.prepareShip.Handle(ctx, orderID)
	require.ErrorIs(t, err, errAborted, "中断が伝播する")
	logCounts(t, pool, "ロールバック後", "ordering.shipments")
	assert.Zero(t, countRows(t, pool, "ordering.shipments"), "ロールバック後の shipments")

	// 正側: コミットすると 1 行になり、読み取り経路からも見える。
	view, err := f.prepareShip.Handle(ctx, orderID)
	require.NoError(t, err, "PrepareShipment 失敗")
	logCounts(t, pool, "コミット後", "ordering.shipments")
	assert.Equal(t, 1, countRows(t, pool, "ordering.shipments"), "コミット後の shipments")
	assert.Equal(t, "preparing", view.Status)
	assert.Equal(t, 1, view.Version)

	// 発送済み化も実 DB で通る（楽観的排他の版が進む）。
	shipped, err := f.markShipped.Handle(ctx, view.ID, "TRACK-1")
	require.NoError(t, err, "MarkShipped 失敗")
	assert.Equal(t, "shipped", shipped.Status)
	assert.Equal(t, "TRACK-1", shipped.TrackingNumber)
	assert.Equal(t, 2, shipped.Version)

	got, err := f.getShip.Handle(ctx, view.ID)
	require.NoError(t, err, "GetShipment 失敗")
	assert.Equal(t, shipped, got, "読み取り経路の結果は書き込み直後の状態と一致する")

	// **出荷は注文を書かない。** 注文の版は据え置きのままである（1 トランザクション 1 集約）。
	order, err := f.get.Handle(ctx, orderID)
	require.NoError(t, err, "Get 失敗")
	assert.Equal(t, 1, order.Version, "注文の版は出荷によって進まない")
}
