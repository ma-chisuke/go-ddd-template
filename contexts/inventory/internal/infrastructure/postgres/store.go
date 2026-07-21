// Package postgres は PostgreSQL（pgx + sqlc）を用いたインフラストラクチャ層の
// アダプタを提供する。application 層のポート（StockStore、UnitOfWork）を実装する。
package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/example/go-ddd-template/contexts/inventory/internal/domain/inventory"
	"github.com/example/go-ddd-template/contexts/inventory/internal/infrastructure/postgres/sqlcgen"
	"github.com/example/go-ddd-template/shared/uow"
)

// pgUniqueViolation は PostgreSQL の一意制約違反を表す SQLSTATE。
const pgUniqueViolation = "23505"

// stockStore は sqlc が生成した Queries を用いた StockStore アダプタ。
// sqlcgen.Queries は *pgxpool.Pool でも pgx.Tx でも動作するため、
// 読み取り用（プール直結）と書き込み用（トランザクション束縛）の両方でこの型を使える。
type stockStore struct {
	q *sqlcgen.Queries
}

func newStockStore(q *sqlcgen.Queries) *stockStore {
	return &stockStore{q: q}
}

// Load は SKU で在庫項目を読み込み、集約を復元する。
// 行が存在しない場合は inventory.ErrStockItemNotFound を返す。
func (s *stockStore) Load(ctx context.Context, sku inventory.SKU) (*inventory.StockItem, error) {
	row, err := s.q.GetStockItemBySKU(ctx, sku.String())
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("SKU %q: %w", sku.String(), inventory.ErrStockItemNotFound)
		}
		return nil, fmt.Errorf("在庫項目の読み込みに失敗しました: %w", err)
	}

	qty, err := inventory.NewQuantity(int(row.Available))
	if err != nil {
		return nil, fmt.Errorf("永続化された数量が不正です（SKU=%q）: %w", sku.String(), err)
	}
	loadedSKU, err := inventory.NewSKU(row.Sku)
	if err != nil {
		return nil, fmt.Errorf("永続化された SKU が不正です: %w", err)
	}
	return inventory.ReconstituteStockItem(row.ID, loadedSKU, qty, int(row.Version)), nil
}

// Save は在庫項目を永続化する。version が 0 の集約は新規挿入し、
// それ以外は楽観的排他制御つきで更新する。版が食い違えば uow.ErrConcurrencyConflict を返す。
func (s *stockStore) Save(ctx context.Context, items ...*inventory.StockItem) error {
	for _, item := range items {
		if err := s.saveOne(ctx, item); err != nil {
			return err
		}
	}
	return nil
}

func (s *stockStore) saveOne(ctx context.Context, item *inventory.StockItem) error {
	if item.Version() == 0 {
		// 新規挿入。永続化済みのバージョンは 1 から始める。
		err := s.q.InsertStockItem(ctx, sqlcgen.InsertStockItemParams{
			ID:        item.ID(),
			Sku:       item.SKU().String(),
			Available: int32(item.Available().Int()),
			Version:   1,
		})
		if err != nil {
			// 同一 SKU の同時挿入は一意制約違反になる。再試行で解決できるよう
			// 楽観的排他制御の衝突へ翻訳する（再試行時に既存行を読み直して更新する）。
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation {
				return fmt.Errorf("SKU %q は既に存在します: %w", item.SKU().String(), uow.ErrConcurrencyConflict)
			}
			return fmt.Errorf("在庫項目の挿入に失敗しました: %w", err)
		}
		item.MarkPersisted(1)
		return nil
	}

	// 既存項目の更新。期待バージョンが一致する行だけを更新する。
	next := item.Version() + 1
	rows, err := s.q.UpdateStockItem(ctx, sqlcgen.UpdateStockItemParams{
		Available: int32(item.Available().Int()),
		Version:   int32(next),
		Sku:       item.SKU().String(),
		Version_2: int32(item.Version()),
	})
	if err != nil {
		return fmt.Errorf("在庫項目の更新に失敗しました: %w", err)
	}
	if rows == 0 {
		// 更新対象が 0 行 = 期待バージョンの行が無い = 他者が先に更新した（衝突）。
		return fmt.Errorf("SKU %q のバージョンが一致しません: %w", item.SKU().String(), uow.ErrConcurrencyConflict)
	}
	item.MarkPersisted(next)
	return nil
}
