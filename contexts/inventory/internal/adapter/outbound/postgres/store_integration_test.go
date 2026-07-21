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
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
	require.NoError(t, err, "プール生成")
	t.Cleanup(pool.Close)

	// stock_reservations は stock_items を参照するため CASCADE で一括初期化する。
	_, err = pool.Exec(ctx, "TRUNCATE inventory.stock_items, inventory.outbox CASCADE")
	require.NoError(t, err, "テーブル初期化（スキーマ未適用の可能性）")
	return pool
}

func mustSKU(t *testing.T, s string) inventory.SKU {
	t.Helper()
	sku, err := inventory.NewSKU(s)
	require.NoError(t, err, "SKU 生成")
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

func newPgFixture(t *testing.T, pool *pgxpool.Pool, _ application.Clock) pgFixture {
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
	require.NoError(t, err, "Replenish")
	assert.Equal(t, 7, res.Available)
	assert.Equal(t, 1, res.Version)

	view, err := f.viewer.QueryStock(ctx, application.QueryStockInput{SKU: "PGX-1"})
	require.NoError(t, err, "QueryStock")
	assert.Equal(t, 7, view.Available)
	assert.Equal(t, 1, view.Version)

	_, err = f.viewer.QueryStock(ctx, application.QueryStockInput{SKU: "MISSING"})
	require.ErrorIs(t, err, inventory.ErrStockItemNotFound)
}

func TestPostgres_ReserveConfirmRelease(t *testing.T) {
	ctx := context.Background()
	pool := setupPool(t)
	f := newPgFixture(t, pool, application.SystemClock{})

	for _, sku := range []string{"PGX-A", "PGX-B"} {
		_, err := f.replenisher.Replenish(ctx, application.ReplenishInput{SKU: sku, Quantity: 10})
		require.NoError(t, err, "補充")
	}

	// マルチ SKU 予約。
	err := f.reserver.Reserve(ctx, application.ReserveInput{
		Ref:   "ORDER-1",
		Lines: []application.ReserveLine{{SKU: "PGX-A", Quantity: 3}, {SKU: "PGX-B", Quantity: 7}},
	})
	require.NoError(t, err, "Reserve")
	viewA, _ := f.viewer.QueryStock(ctx, application.QueryStockInput{SKU: "PGX-A"})
	assert.Equal(t, 7, viewA.Available, "A 予約後 available")
	assert.Equal(t, 3, viewA.Reserved, "A 予約後 reserved")

	// 確定 → available 不変。
	require.NoError(t, f.confirmer.Confirm(ctx, "ORDER-1"), "Confirm")
	viewB, _ := f.viewer.QueryStock(ctx, application.QueryStockInput{SKU: "PGX-B"})
	assert.Equal(t, 3, viewB.Available, "B 確定後 available")
	assert.Equal(t, 7, viewB.Reserved, "B 確定後 reserved")

	// 解放 → available へ戻る。
	require.NoError(t, f.releaser.Release(ctx, "ORDER-1"), "Release")
	viewA, _ = f.viewer.QueryStock(ctx, application.QueryStockInput{SKU: "PGX-A"})
	viewB, _ = f.viewer.QueryStock(ctx, application.QueryStockInput{SKU: "PGX-B"})
	assert.Equal(t, 0, viewA.Reserved, "A 解放後 reserved")
	assert.Equal(t, 10, viewA.Available, "A 解放後 available")
	assert.Equal(t, 0, viewB.Reserved, "B 解放後 reserved")
	assert.Equal(t, 10, viewB.Available, "B 解放後 available")
}

func TestPostgres_ReaperReleasesExpiredPending(t *testing.T) {
	ctx := context.Background()
	pool := setupPool(t)
	// 擬似時計を実時刻より十分未来に置き、TTL 1 時間の pending を確実に期限切れにする。
	clock := testutil.NewClock(time.Now().Add(2 * time.Hour))
	f := newPgFixture(t, pool, clock)
	log := testLogger()

	_, err := f.replenisher.Replenish(ctx, application.ReplenishInput{SKU: "PGX-R", Quantity: 100})
	require.NoError(t, err, "補充")
	require.NoError(t, f.reserver.Reserve(ctx, application.ReserveInput{Ref: "PENDING", Lines: []application.ReserveLine{{SKU: "PGX-R", Quantity: 10}}}), "pending 予約")
	require.NoError(t, f.reserver.Reserve(ctx, application.ReserveInput{Ref: "CONFIRMED", Lines: []application.ReserveLine{{SKU: "PGX-R", Quantity: 20}}}), "confirmed 予約")
	require.NoError(t, f.confirmer.Confirm(ctx, "CONFIRMED"), "Confirm")

	reaper := application.NewReaper(uow.NewExecutor(), f.work, application.NewInProcessDispatcher(log), clock, log, 100)
	require.NoError(t, reaper.Sweep(ctx), "Sweep")

	view, _ := f.viewer.QueryStock(ctx, application.QueryStockInput{SKU: "PGX-R"})
	// PENDING(10) が戻り、CONFIRMED(20) は残る。
	assert.Equal(t, 80, view.Available, "Reap 後 available")
	assert.Equal(t, 20, view.Reserved, "Reap 後 reserved")
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
	require.NoError(t, err, "UoW")

	// 送信中継が未送信を送出して published にする。
	store := postgres.NewOutboxStore(pool)
	pub := &countingPublisher{}
	runner := outbox.NewRunner(store, pub, log, outbox.WithBatch(10))
	sent, err := runner.RunOnce(ctx)
	require.NoError(t, err, "RunOnce")
	assert.Equal(t, 1, sent, "送出件数")
	assert.Equal(t, 1, pub.count, "publish 件数")
	// 2 回目は送信済みなので 0 件。
	sent, err = runner.RunOnce(ctx)
	require.NoError(t, err, "2 回目 RunOnce")
	assert.Equal(t, 0, sent, "2 回目の送出件数")
}

func TestPostgres_ConcurrencyConflict(t *testing.T) {
	ctx := context.Background()
	pool := setupPool(t)
	f := newPgFixture(t, pool, application.SystemClock{})
	read := postgres.NewReadStockStore(pool)

	_, err := f.replenisher.Replenish(ctx, application.ReplenishInput{SKU: "PGX-CONFLICT", Quantity: 5})
	require.NoError(t, err, "初期補充")

	sku := mustSKU(t, "PGX-CONFLICT")
	stale := loadVia(t, read, sku) // version 1
	other := loadVia(t, read, sku) // version 1

	q1, _ := inventory.NewQuantity(1)

	_ = other.Replenish(q1)
	require.NoError(t, f.work.Within(ctx, func(ctx context.Context, r application.Repos) error {
		return r.Stock().Save(ctx, other)
	}), "other の保存")

	_ = stale.Replenish(q1)
	err = f.work.Within(ctx, func(ctx context.Context, r application.Repos) error {
		return r.Stock().Save(ctx, stale)
	})
	require.ErrorIs(t, err, uow.ErrConcurrencyConflict)
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
	require.NoError(t, err, "読み込み")
	return item
}
