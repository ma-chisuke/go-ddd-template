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
	"io"
	"log/slog"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/example/go-ddd-template/contexts/ordering/internal/adapter/outbound/postgres"
	"github.com/example/go-ddd-template/contexts/ordering/internal/application"
	"github.com/example/go-ddd-template/contexts/ordering/internal/domain/order"
	"github.com/example/go-ddd-template/contexts/ordering/port"
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
	_, err = pool.Exec(ctx, "TRUNCATE ordering.orders, ordering.outbox CASCADE")
	require.NoError(t, err, "テーブル初期化に失敗（スキーマ未適用の可能性）")
	return pool
}

// pgFixture は postgres アダプタで注文ユースケース一式を組み立てる。
type pgFixture struct {
	place  *application.PlaceOrder
	get    *application.GetOrder
	cancel *application.CancelOrder
	work   *postgres.UnitOfWork
}

func newPgFixture(t *testing.T, pool *pgxpool.Pool) pgFixture {
	t.Helper()
	log := testLogger()
	work := postgres.NewUnitOfWork(pool)
	exec := uow.NewExecutor()
	dispatcher := application.NewInProcessDispatcher(log)
	return pgFixture{
		place:  application.NewPlaceOrder(exec, work, okReserver{}, dispatcher, log),
		get:    application.NewGetOrder(postgres.NewReadOrderStore(pool), log),
		cancel: application.NewCancelOrder(exec, work, log),
		work:   work,
	}
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
	// 2 回目は送信済みなので 0 件。
	sent, err = runner.RunOnce(ctx)
	require.NoError(t, err, "2 回目 RunOnce 失敗")
	assert.Zero(t, sent, "2 回目の送出件数")
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
func loadVia(t *testing.T, read application.OrderStore, id order.OrderID) *order.Order {
	t.Helper()
	o, err := read.Load(context.Background(), id)
	require.NoError(t, err, "読み込み 失敗")
	return o
}
