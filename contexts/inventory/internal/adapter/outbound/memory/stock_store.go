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
type stockStore struct {
	tx *txState
}

// コンパイル時にポートを満たしていることを確認する。
var _ application.StockStore = (*stockStore)(nil)

func (s *stockStore) Load(_ context.Context, sku domain.SKU) (*domain.StockItem, error) {
	return s.tx.stockRows.load(sku)
}

func (s *stockStore) LoadMany(_ context.Context, skus []domain.SKU) ([]*domain.StockItem, error) {
	return s.tx.stockRows.loadMany(skus)
}

func (s *stockStore) LoadByReservation(_ context.Context, ref domain.ReservationRef) ([]*domain.StockItem, error) {
	return s.tx.stockRows.loadByReservation(ref)
}

func (s *stockStore) LoadExpiredPending(_ context.Context, before time.Time, limit int) ([]*domain.StockItem, error) {
	return s.tx.stockRows.loadExpiredPending(before, limit)
}

// Save は各集約の版を確定ストアと突き合わせて検証し、集約のバージョンを同期（MarkPersisted）
// したうえで、確定ストアへ書き込む行（予約状態を含む）を staging に積む。実際の書き込みは
// コミット時に行う。版が食い違えば uow.ErrConcurrencyConflict を返し、確定ストアは変更しない。
//
// マルチ SKU 予約（items が複数）では、全件の版チェックと staging への積み込みを 1 回の
// ロックの中で行う。途中で他のトランザクションが割り込めないことを、全か無かの予約が
// 前提にしている。
func (s *stockStore) Save(_ context.Context, items ...*domain.StockItem) error {
	s.tx.stockRows.mu.Lock()
	defer s.tx.stockRows.mu.Unlock()

	for _, item := range items {
		existing, ok := s.tx.stockRows.rows[item.SKU().String()]
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

		s.tx.staged = append(s.tx.staged, stockItemToRow(item, next))
		item.MarkPersisted(next)
	}
	return nil
}

// NewReadStockStore は読み取り用の StockStore を返す。
// 在庫照会ユースケース用。書き込みには使わない。
func NewReadStockStore(stockRows *StockRows) application.StockStore {
	return &readStockStore{stockRows: stockRows}
}

// readStockStore は確定済みデータを直接読む読み取り専用アダプタ。
type readStockStore struct {
	stockRows *StockRows
}

var _ application.StockStore = (*readStockStore)(nil)

func (s *readStockStore) Load(_ context.Context, sku domain.SKU) (*domain.StockItem, error) {
	return s.stockRows.load(sku)
}

func (s *readStockStore) LoadMany(_ context.Context, skus []domain.SKU) ([]*domain.StockItem, error) {
	return s.stockRows.loadMany(skus)
}

func (s *readStockStore) LoadByReservation(_ context.Context, ref domain.ReservationRef) ([]*domain.StockItem, error) {
	return s.stockRows.loadByReservation(ref)
}

func (s *readStockStore) LoadExpiredPending(_ context.Context, before time.Time, limit int) ([]*domain.StockItem, error) {
	return s.stockRows.loadExpiredPending(before, limit)
}

// Save は読み取り専用アダプタでは使用しない。誤用を早期に検知するためエラーを返す。
func (s *readStockStore) Save(_ context.Context, _ ...*domain.StockItem) error {
	return fmt.Errorf("readStockStore は読み取り専用です: 書き込みは UnitOfWork.Within を使ってください")
}
