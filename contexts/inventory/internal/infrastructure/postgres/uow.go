package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/example/go-ddd-template/contexts/inventory/internal/application"
	"github.com/example/go-ddd-template/contexts/inventory/internal/infrastructure/postgres/sqlcgen"
)

// repos は application.Repos の実装。ひとつのトランザクションに束ねた
// リポジトリの束を保持する。
type repos struct {
	stock application.StockStore
}

func (r repos) Stock() application.StockStore { return r.stock }

// UnitOfWork は pgxpool を用いた実トランザクションの作業単位。
// Within がトランザクションを開き、そのトランザクションに束ねた Repos を組み立てて
// コールバックへ渡す。トランザクション境界の所有者は Within である。
type UnitOfWork struct {
	pool *pgxpool.Pool
}

// NewUnitOfWork は書き込み用の作業単位を生成する。
func NewUnitOfWork(pool *pgxpool.Pool) *UnitOfWork {
	return &UnitOfWork{pool: pool}
}

// コンパイル時にポートを満たしていることを確認する。
var _ application.UnitOfWork = (*UnitOfWork)(nil)

// Within はトランザクションを開始し、fn が nil を返せばコミット、
// エラーを返せばロールバックする。
func (u *UnitOfWork) Within(ctx context.Context, fn func(ctx context.Context, r application.Repos) error) error {
	tx, err := u.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("トランザクションの開始に失敗しました: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			// コミット済みなら Rollback は no-op。念のため常に呼ぶ。
			_ = tx.Rollback(ctx)
		}
	}()

	// トランザクションに束ねた Queries から Repos を組み立てる。
	r := repos{stock: newStockStore(sqlcgen.New(tx))}
	if err := fn(ctx, r); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("コミットに失敗しました: %w", err)
	}
	committed = true
	return nil
}

// NewReadStockStore はコネクションプールに直結した読み取り用 StockStore を返す。
// 読み取り専用ユースケース（在庫照会）で用いる。書き込みには使わないこと
// （書き込みは必ず UnitOfWork.Within の内側で行う）。
func NewReadStockStore(pool *pgxpool.Pool) application.StockStore {
	return newStockStore(sqlcgen.New(pool))
}
