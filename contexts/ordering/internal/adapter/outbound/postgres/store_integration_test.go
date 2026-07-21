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

	"github.com/jackc/pgx/v5/pgxpool"

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
	if err != nil {
		t.Fatalf("プール生成に失敗: %v", err)
	}
	t.Cleanup(pool.Close)

	// order_lines は orders を参照するため CASCADE で一括初期化する。
	if _, err := pool.Exec(ctx, "TRUNCATE ordering.orders, ordering.outbox CASCADE"); err != nil {
		t.Fatalf("テーブル初期化に失敗（スキーマ未適用の可能性）: %v", err)
	}
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
	if err != nil {
		t.Fatalf("Place 失敗: %v", err)
	}
	view, err := f.get.Handle(ctx, id.String())
	if err != nil {
		t.Fatalf("Get 失敗: %v", err)
	}
	if view.Status != "confirmed" || view.Version != 1 {
		t.Fatalf("保存後の注文が不正: %+v", view)
	}
	if view.TotalAmount != 4100 || len(view.Lines) != 2 {
		t.Fatalf("合計/明細が不正: %+v", view)
	}

	// ConfirmReservation コマンドが outbox に積まれている。
	store := postgres.NewOutboxStore(pool)
	msgs, err := store.Unpublished(ctx, 10)
	if err != nil {
		t.Fatalf("Unpublished 失敗: %v", err)
	}
	if len(msgs) != 1 || msgs[0].Type != application.MessageTypeConfirmReservation {
		t.Fatalf("outbox の内容が不正: %+v", msgs)
	}
}

func TestPostgres_CancelEnqueuesOrderCancelled(t *testing.T) {
	ctx := context.Background()
	pool := setupPool(t)
	f := newPgFixture(t, pool)

	id, err := f.place.Handle(ctx, sampleInput())
	if err != nil {
		t.Fatalf("Place 失敗: %v", err)
	}
	if err := f.cancel.Handle(ctx, id.String()); err != nil {
		t.Fatalf("Cancel 失敗: %v", err)
	}
	view, _ := f.get.Handle(ctx, id.String())
	if view.Status != "cancelled" || view.Version != 2 {
		t.Fatalf("取消後の注文が不正: %+v", view)
	}

	// ConfirmReservation と OrderCancelled の 2 件が積まれている。
	store := postgres.NewOutboxStore(pool)
	msgs, _ := store.Unpublished(ctx, 10)
	var hasCancelled bool
	for _, m := range msgs {
		if m.Type == application.MessageTypeOrderCancelled {
			hasCancelled = true
		}
	}
	if !hasCancelled {
		t.Fatalf("OrderCancelled が積まれていない: %+v", msgs)
	}
}

func TestPostgres_OutboxRelay(t *testing.T) {
	ctx := context.Background()
	pool := setupPool(t)
	f := newPgFixture(t, pool)

	if _, err := f.place.Handle(ctx, sampleInput()); err != nil {
		t.Fatalf("Place 失敗: %v", err)
	}

	store := postgres.NewOutboxStore(pool)
	pub := &countingPublisher{}
	runner := outbox.NewRunner(store, pub, testLogger(), outbox.WithBatch(10))
	sent, err := runner.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce 失敗: %v", err)
	}
	if sent != 1 || pub.count != 1 {
		t.Fatalf("送出件数が不正: sent=%d published=%d", sent, pub.count)
	}
	// 2 回目は送信済みなので 0 件。
	sent, err = runner.RunOnce(ctx)
	if err != nil {
		t.Fatalf("2 回目 RunOnce 失敗: %v", err)
	}
	if sent != 0 {
		t.Fatalf("2 回目の送出件数 = %d, want 0", sent)
	}
}

func TestPostgres_ConcurrencyConflict(t *testing.T) {
	ctx := context.Background()
	pool := setupPool(t)
	f := newPgFixture(t, pool)
	read := postgres.NewReadOrderStore(pool)

	id, err := f.place.Handle(ctx, sampleInput())
	if err != nil {
		t.Fatalf("Place 失敗: %v", err)
	}

	// 同一注文の 2 つの版 1 コピーを読み出す。
	stale := loadVia(t, read, id) // version 1
	other := loadVia(t, read, id) // version 1

	// 先に other を取消して保存（version 2 になる）。
	_ = other.Cancel()
	if err := f.work.Within(ctx, func(ctx context.Context, r application.Repos) error {
		return r.Orders().Save(ctx, other)
	}); err != nil {
		t.Fatalf("other の保存 失敗: %v", err)
	}

	// stale（version 1）を取消して保存すると版が食い違い衝突する。
	_ = stale.Cancel()
	err = f.work.Within(ctx, func(ctx context.Context, r application.Repos) error {
		return r.Orders().Save(ctx, stale)
	})
	if !errors.Is(err, uow.ErrConcurrencyConflict) {
		t.Fatalf("エラー = %v, want ErrConcurrencyConflict", err)
	}
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
	if err != nil {
		t.Fatalf("読み込み 失敗: %v", err)
	}
	return o
}
