package memory

import (
	"context"
	"fmt"
	"time"

	"github.com/example/go-ddd-template/contexts/inventory/internal/application"
	"github.com/example/go-ddd-template/contexts/inventory/internal/domain"
	"github.com/example/go-ddd-template/shared/uow"
)

// stockStore はトランザクションに束ねた StockStore。
// 読み取りは確定済みの行を見る（同一トランザクションで staging した行は見えない）。
type stockStore struct {
	rows    *StockRows
	staging *staging[stockRow]
}

// コンパイル時にポートを満たしていることを確認する。
var _ application.StockStore = (*stockStore)(nil)

func (s *stockStore) Load(_ context.Context, sku domain.SKU) (*domain.StockItem, error) {
	return loadStockItem(s.rows, sku)
}

func (s *stockStore) LoadMany(_ context.Context, skus []domain.SKU) ([]*domain.StockItem, error) {
	return loadStockItemsBySKU(s.rows, skus)
}

func (s *stockStore) LoadByReservation(_ context.Context, ref domain.ReservationRef) ([]*domain.StockItem, error) {
	return loadStockItemsByReservation(s.rows, ref)
}

func (s *stockStore) LoadExpiredPending(_ context.Context, before time.Time, limit int) ([]*domain.StockItem, error) {
	return loadExpiredPendingStockItems(s.rows, before, limit)
}

// Save は各集約の版を確定済みの行と突き合わせて検証し、集約のバージョンを同期
// （MarkPersisted）したうえで、書き込む行（予約状態を含む）を staging に積む。実際の
// 書き込みはコミット時に行う。版が食い違えば uow.ErrConcurrencyConflict を返し、
// 確定済みの行は変更しない。
//
// マルチ SKU 予約（items が複数）では、全件の版チェックと staging への積み込みを
// withLock による 1 回のロックの中で行う。途中で他のトランザクションが割り込めないことを
// 「全か無か」の予約が前提にしている。行ごとに get してから積む形へ崩すと、
// 版チェックと積み込みの間に割り込みの窓ができる。
func (s *stockStore) Save(_ context.Context, items ...*domain.StockItem) error {
	return s.rows.withLock(func(m map[string]stockRow) error {
		for _, item := range items {
			existing, ok := m[item.SKU().String()]
			var next int
			if item.Version() == 0 {
				// 新規挿入。既に存在するなら衝突。
				if ok {
					return fmt.Errorf("SKU %q は既に存在します: %w", item.SKU().String(), uow.ErrConcurrencyConflict)
				}
				next = 1
			} else {
				// 既存更新。存在しない、または版が食い違えば衝突。
				if !ok || existing.version != item.Version() {
					return fmt.Errorf("SKU %q のバージョンが一致しません: %w", item.SKU().String(), uow.ErrConcurrencyConflict)
				}
				next = item.Version() + 1
			}

			s.staging.stage(item.SKU().String(), stockItemToRow(item, next))
			item.MarkPersisted(next)
		}
		return nil
	})
}

// NewReadStockStore は読み取り用の StockStore を返す。
// 在庫照会ユースケース用。書き込みには使わない。
func NewReadStockStore(stockRows *StockRows) application.StockStore {
	return &readStockStore{rows: stockRows}
}

// readStockStore は確定済みデータを直接読む読み取り専用アダプタ。
type readStockStore struct {
	rows *StockRows
}

var _ application.StockStore = (*readStockStore)(nil)

func (s *readStockStore) Load(_ context.Context, sku domain.SKU) (*domain.StockItem, error) {
	return loadStockItem(s.rows, sku)
}

func (s *readStockStore) LoadMany(_ context.Context, skus []domain.SKU) ([]*domain.StockItem, error) {
	return loadStockItemsBySKU(s.rows, skus)
}

func (s *readStockStore) LoadByReservation(_ context.Context, ref domain.ReservationRef) ([]*domain.StockItem, error) {
	return loadStockItemsByReservation(s.rows, ref)
}

func (s *readStockStore) LoadExpiredPending(_ context.Context, before time.Time, limit int) ([]*domain.StockItem, error) {
	return loadExpiredPendingStockItems(s.rows, before, limit)
}

// Save は読み取り専用アダプタでは使用しない。誤用を早期に検知するためエラーを返す。
func (s *readStockStore) Save(_ context.Context, _ ...*domain.StockItem) error {
	return fmt.Errorf("readStockStore は読み取り専用です: 書き込みは UnitOfWork.Within を使ってください")
}

// 以下は集約固有の読み取り。共通機構 rows[R] は stockRow の中身（SKU・予約参照・期限・版）を
// 知らないため、索引と復元はここに置く。複数行を見る読み取りは withLock で 1 回のロックの
// まま走査し、確定の途中の状態を観測しないようにする。

// loadStockItem は SKU に対応する在庫項目を読み込み、集約を復元する。
func loadStockItem(stockRows *StockRows, sku domain.SKU) (*domain.StockItem, error) {
	r, ok := stockRows.get(sku.String())
	if !ok {
		return nil, fmt.Errorf("SKU %q: %w", sku.String(), domain.ErrStockItemNotFound)
	}
	return stockRowToStockItem(r)
}

// loadStockItemsBySKU は複数 SKU をまとめて読み込む。見つからない SKU は黙って除外する
// （存在検査はドメインサービス側の事前検証が担う）。
func loadStockItemsBySKU(stockRows *StockRows, skus []domain.SKU) ([]*domain.StockItem, error) {
	items := make([]*domain.StockItem, 0, len(skus))
	err := stockRows.withLock(func(m map[string]stockRow) error {
		for _, sku := range skus {
			r, ok := m[sku.String()]
			if !ok {
				continue
			}
			item, err := stockRowToStockItem(r)
			if err != nil {
				return err
			}
			items = append(items, item)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return items, nil
}

// loadStockItemsByReservation は指定参照を持つ全ての在庫項目を読み込む。
func loadStockItemsByReservation(stockRows *StockRows, ref domain.ReservationRef) ([]*domain.StockItem, error) {
	var items []*domain.StockItem
	err := stockRows.withLock(func(m map[string]stockRow) error {
		for _, r := range m {
			if !stockRowHasReservation(r, ref.String()) {
				continue
			}
			item, err := stockRowToStockItem(r)
			if err != nil {
				return err
			}
			items = append(items, item)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return items, nil
}

// loadExpiredPendingStockItems は before 時点で期限切れの pending 予約を持つ在庫項目を
// 最大 limit 件返す。
func loadExpiredPendingStockItems(stockRows *StockRows, before time.Time, limit int) ([]*domain.StockItem, error) {
	var items []*domain.StockItem
	err := stockRows.withLock(func(m map[string]stockRow) error {
		for _, r := range m {
			if !stockRowHasExpiredPending(r, before) {
				continue
			}
			item, err := stockRowToStockItem(r)
			if err != nil {
				return err
			}
			items = append(items, item)
			if limit > 0 && len(items) >= limit {
				break
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return items, nil
}
