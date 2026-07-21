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

	"github.com/example/go-ddd-template/contexts/inventory/internal/adapter/outbound/postgres"
	"github.com/example/go-ddd-template/contexts/inventory/internal/application"
	"github.com/example/go-ddd-template/contexts/inventory/internal/domain/inventory"
	"github.com/example/go-ddd-template/shared/outbox"
	"github.com/example/go-ddd-template/shared/testutil"
	"github.com/example/go-ddd-template/shared/uow"
)

func testLogger() *slog.Logger { return slog.New(slog.NewJSONHandler(io.Discard, nil)) }

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

	// stock_reservations は stock_items を参照するため CASCADE で一括初期化する。
	if _, err := pool.Exec(ctx, "TRUNCATE inventory.stock_items, inventory.outbox CASCADE"); err != nil {
		t.Fatalf("テーブル初期化に失敗（スキーマ未適用の可能性）: %v", err)
	}
	return pool
}

func mustSKU(t *testing.T, s string) inventory.SKU {
	t.Helper()
	sku, err := inventory.NewSKU(s)
	if err != nil {
		t.Fatalf("SKU 生成失敗: %v", err)
	}
	return sku
}

// pgFixture は postgres アダプタで予約系ユースケース一式を組み立てる。
type pgFixture struct {
	replenisher *application.Replenisher
	reserver    *application.Reserver
	confirmer   *application.Confirmer
	releaser    *application.Releaser
	viewer      *application.StockViewer
	work        *postgres.UnitOfWork
}

func newPgFixture(t *testing.T, pool *pgxpool.Pool, clock application.Clock) pgFixture {
	t.Helper()
	log := testLogger()
	work := postgres.NewUnitOfWork(pool)
	exec := uow.NewExecutor()
	dispatcher := application.NewInProcessDispatcher(log)
	return pgFixture{
		replenisher: application.NewReplenisher(exec, work, dispatcher, log),
		reserver:    application.NewReserver(exec, work, dispatcher, log, time.Hour),
		confirmer:   application.NewConfirmer(exec, work, dispatcher, log),
		releaser:    application.NewReleaser(exec, work, dispatcher, log),
		viewer:      application.NewStockViewer(postgres.NewReadStockStore(pool), log),
		work:        work,
	}
}

func TestPostgres_ReplenishThenQuery(t *testing.T) {
	ctx := context.Background()
	pool := setupPool(t)
	f := newPgFixture(t, pool, application.SystemClock{})

	res, err := f.replenisher.Replenish(ctx, application.ReplenishInput{SKU: "PGX-1", Quantity: 7})
	if err != nil {
		t.Fatalf("Replenish 失敗: %v", err)
	}
	if res.Available != 7 || res.Version != 1 {
		t.Fatalf("補充結果が不正: %+v", res)
	}

	view, err := f.viewer.QueryStock(ctx, application.QueryStockInput{SKU: "PGX-1"})
	if err != nil {
		t.Fatalf("QueryStock 失敗: %v", err)
	}
	if view.Available != 7 || view.Version != 1 {
		t.Fatalf("照会結果が不正: %+v", view)
	}

	if _, err := f.viewer.QueryStock(ctx, application.QueryStockInput{SKU: "MISSING"}); !errors.Is(err, inventory.ErrStockItemNotFound) {
		t.Fatalf("エラー = %v, want ErrStockItemNotFound", err)
	}
}

func TestPostgres_ReserveConfirmRelease(t *testing.T) {
	ctx := context.Background()
	pool := setupPool(t)
	f := newPgFixture(t, pool, application.SystemClock{})

	for _, sku := range []string{"PGX-A", "PGX-B"} {
		if _, err := f.replenisher.Replenish(ctx, application.ReplenishInput{SKU: sku, Quantity: 10}); err != nil {
			t.Fatalf("補充失敗: %v", err)
		}
	}

	// マルチ SKU 予約。
	if err := f.reserver.Reserve(ctx, application.ReserveInput{
		Ref:   "ORDER-1",
		Lines: []application.ReserveLine{{SKU: "PGX-A", Quantity: 3}, {SKU: "PGX-B", Quantity: 7}},
	}); err != nil {
		t.Fatalf("Reserve 失敗: %v", err)
	}
	viewA, _ := f.viewer.QueryStock(ctx, application.QueryStockInput{SKU: "PGX-A"})
	if viewA.Available != 7 || viewA.Reserved != 3 {
		t.Fatalf("A の予約後が不正: %+v", viewA)
	}

	// 確定 → available 不変。
	if err := f.confirmer.Confirm(ctx, "ORDER-1"); err != nil {
		t.Fatalf("Confirm 失敗: %v", err)
	}
	viewB, _ := f.viewer.QueryStock(ctx, application.QueryStockInput{SKU: "PGX-B"})
	if viewB.Available != 3 || viewB.Reserved != 7 {
		t.Fatalf("B の確定後が不正: %+v", viewB)
	}

	// 解放 → available へ戻る。
	if err := f.releaser.Release(ctx, "ORDER-1"); err != nil {
		t.Fatalf("Release 失敗: %v", err)
	}
	viewA, _ = f.viewer.QueryStock(ctx, application.QueryStockInput{SKU: "PGX-A"})
	viewB, _ = f.viewer.QueryStock(ctx, application.QueryStockInput{SKU: "PGX-B"})
	if viewA.Reserved != 0 || viewA.Available != 10 || viewB.Reserved != 0 || viewB.Available != 10 {
		t.Fatalf("解放後が不正: A=%+v B=%+v", viewA, viewB)
	}
}

func TestPostgres_ReaperReleasesExpiredPending(t *testing.T) {
	ctx := context.Background()
	pool := setupPool(t)
	// 擬似時計を実時刻より十分未来に置き、TTL 1 時間の pending を確実に期限切れにする。
	clock := testutil.NewClock(time.Now().Add(2 * time.Hour))
	f := newPgFixture(t, pool, clock)
	log := testLogger()

	if _, err := f.replenisher.Replenish(ctx, application.ReplenishInput{SKU: "PGX-R", Quantity: 100}); err != nil {
		t.Fatalf("補充失敗: %v", err)
	}
	if err := f.reserver.Reserve(ctx, application.ReserveInput{Ref: "PENDING", Lines: []application.ReserveLine{{SKU: "PGX-R", Quantity: 10}}}); err != nil {
		t.Fatalf("pending 予約失敗: %v", err)
	}
	if err := f.reserver.Reserve(ctx, application.ReserveInput{Ref: "CONFIRMED", Lines: []application.ReserveLine{{SKU: "PGX-R", Quantity: 20}}}); err != nil {
		t.Fatalf("confirmed 予約失敗: %v", err)
	}
	if err := f.confirmer.Confirm(ctx, "CONFIRMED"); err != nil {
		t.Fatalf("Confirm 失敗: %v", err)
	}

	reaper := application.NewReaper(uow.NewExecutor(), f.work, application.NewInProcessDispatcher(log), clock, log, 100)
	if err := reaper.Sweep(ctx); err != nil {
		t.Fatalf("Sweep 失敗: %v", err)
	}

	view, _ := f.viewer.QueryStock(ctx, application.QueryStockInput{SKU: "PGX-R"})
	// PENDING(10) が戻り、CONFIRMED(20) は残る。
	if view.Available != 80 || view.Reserved != 20 {
		t.Fatalf("Reap 後が不正: %+v", view)
	}
}

func TestPostgres_OutboxEnqueueAndRelay(t *testing.T) {
	ctx := context.Background()
	pool := setupPool(t)
	log := testLogger()
	work := postgres.NewUnitOfWork(pool)

	// UoW 内で在庫保存とアウトボックス Enqueue を同一トランザクションで行う。
	err := work.Within(ctx, func(ctx context.Context, r application.Repos) error {
		item, err := inventory.NewStockItem("id-obx", mustSKU(t, "PGX-OBX"))
		if err != nil {
			return err
		}
		q, _ := inventory.NewQuantity(5)
		if err := item.Replenish(q); err != nil {
			return err
		}
		if err := r.Stock().Save(ctx, item); err != nil {
			return err
		}
		return r.Outbox().Enqueue(ctx, outbox.Message{
			ID:         "obx-1",
			Type:       "demo.message",
			Payload:    []byte(`{"hello":"world"}`),
			OccurredAt: time.Now().UTC(),
		})
	})
	if err != nil {
		t.Fatalf("UoW 失敗: %v", err)
	}

	// 送信中継が未送信を送出して published にする。
	store := postgres.NewOutboxStore(pool)
	pub := &countingPublisher{}
	runner := outbox.NewRunner(store, pub, log, outbox.WithBatch(10))
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
	f := newPgFixture(t, pool, application.SystemClock{})
	read := postgres.NewReadStockStore(pool)

	if _, err := f.replenisher.Replenish(ctx, application.ReplenishInput{SKU: "PGX-CONFLICT", Quantity: 5}); err != nil {
		t.Fatalf("初期補充 失敗: %v", err)
	}

	sku := mustSKU(t, "PGX-CONFLICT")
	stale := loadVia(t, read, sku) // version 1
	other := loadVia(t, read, sku) // version 1

	q1, _ := inventory.NewQuantity(1)

	_ = other.Replenish(q1)
	if err := f.work.Within(ctx, func(ctx context.Context, r application.Repos) error {
		return r.Stock().Save(ctx, other)
	}); err != nil {
		t.Fatalf("other の保存 失敗: %v", err)
	}

	_ = stale.Replenish(q1)
	err := f.work.Within(ctx, func(ctx context.Context, r application.Repos) error {
		return r.Stock().Save(ctx, stale)
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

// loadVia は読み取りストア経由で集約を読み込む。
func loadVia(t *testing.T, read application.StockStore, sku inventory.SKU) *inventory.StockItem {
	t.Helper()
	item, err := read.Load(context.Background(), sku)
	if err != nil {
		t.Fatalf("読み込み 失敗: %v", err)
	}
	return item
}
