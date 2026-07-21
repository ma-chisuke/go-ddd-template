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

	"github.com/example/go-ddd-template/contexts/inventory/internal/adapter/outbound/postgres"
	"github.com/example/go-ddd-template/contexts/inventory/internal/application"
	"github.com/example/go-ddd-template/contexts/inventory/internal/domain/inventory"
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

	if _, err := pool.Exec(ctx, "TRUNCATE inventory.stock_items"); err != nil {
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

func TestPostgres_ReplenishThenQuery(t *testing.T) {
	ctx := context.Background()
	pool := setupPool(t)
	log := testLogger()

	work := postgres.NewUnitOfWork(pool)
	read := postgres.NewReadStockStore(pool)
	exec := uow.NewExecutor()
	dispatcher := application.NewInProcessDispatcher(log)
	replenisher := application.NewReplenisher(exec, work, dispatcher, log)
	viewer := application.NewStockViewer(read, log)

	// 新規補充 → version 1。
	res, err := replenisher.Replenish(ctx, application.ReplenishInput{SKU: "PGX-1", Quantity: 7})
	if err != nil {
		t.Fatalf("Replenish 失敗: %v", err)
	}
	if res.Available != 7 || res.Version != 1 {
		t.Fatalf("補充結果が不正: %+v", res)
	}

	// 照会。
	view, err := viewer.QueryStock(ctx, application.QueryStockInput{SKU: "PGX-1"})
	if err != nil {
		t.Fatalf("QueryStock 失敗: %v", err)
	}
	if view.Available != 7 || view.Version != 1 {
		t.Fatalf("照会結果が不正: %+v", view)
	}

	// 再補充 → version 2。
	res2, err := replenisher.Replenish(ctx, application.ReplenishInput{SKU: "PGX-1", Quantity: 3})
	if err != nil {
		t.Fatalf("再補充 失敗: %v", err)
	}
	if res2.Available != 10 || res2.Version != 2 {
		t.Fatalf("再補充結果が不正: %+v", res2)
	}

	// 未登録 SKU の照会。
	if _, err := viewer.QueryStock(ctx, application.QueryStockInput{SKU: "MISSING"}); !errors.Is(err, inventory.ErrStockItemNotFound) {
		t.Fatalf("エラー = %v, want ErrStockItemNotFound", err)
	}
}

func TestPostgres_ConcurrencyConflict(t *testing.T) {
	ctx := context.Background()
	pool := setupPool(t)
	log := testLogger()

	work := postgres.NewUnitOfWork(pool)
	read := postgres.NewReadStockStore(pool)
	exec := uow.NewExecutor()
	dispatcher := application.NewInProcessDispatcher(log)
	replenisher := application.NewReplenisher(exec, work, dispatcher, log)

	// version 1 を作る。
	if _, err := replenisher.Replenish(ctx, application.ReplenishInput{SKU: "PGX-CONFLICT", Quantity: 5}); err != nil {
		t.Fatalf("初期補充 失敗: %v", err)
	}

	sku := mustSKU(t, "PGX-CONFLICT")
	// 同じ version 1 の集約を 2 つ読み出す。
	stale := loadVia(t, read, sku) // version 1
	other := loadVia(t, read, sku) // version 1

	q1, _ := inventory.NewQuantity(1)

	// other を保存 → version 2。
	_ = other.Replenish(q1)
	if err := work.Within(ctx, func(ctx context.Context, r application.Repos) error {
		return r.Stock().Save(ctx, other)
	}); err != nil {
		t.Fatalf("other の保存 失敗: %v", err)
	}

	// stale（version 1 のまま）を保存 → 衝突。
	_ = stale.Replenish(q1)
	err := work.Within(ctx, func(ctx context.Context, r application.Repos) error {
		return r.Stock().Save(ctx, stale)
	})
	if !errors.Is(err, uow.ErrConcurrencyConflict) {
		t.Fatalf("エラー = %v, want ErrConcurrencyConflict", err)
	}
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
