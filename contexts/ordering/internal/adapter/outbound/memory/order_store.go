package memory

import (
	"context"
	"fmt"

	"github.com/example/go-ddd-template/contexts/ordering/internal/application"
	"github.com/example/go-ddd-template/contexts/ordering/internal/domain"
	"github.com/example/go-ddd-template/shared/uow"
)

// orderStore はトランザクションに束ねた OrderStore。
type orderStore struct {
	tx *txState
}

// コンパイル時にポートを満たしていることを確認する。
var _ application.OrderStore = (*orderStore)(nil)

func (s *orderStore) Load(_ context.Context, id domain.OrderID) (*domain.Order, error) {
	return s.tx.orderRows.load(id)
}

// Save は集約の版を確定ストアと突き合わせて検証し、集約のバージョンを同期（MarkPersisted）
// したうえで、確定ストアへ書き込む行を staging に積む。実際の書き込みはコミット時に行う。
// 版が食い違えば uow.ErrConcurrencyConflict を返し、確定ストアは変更しない。
func (s *orderStore) Save(_ context.Context, o *domain.Order) error {
	s.tx.orderRows.mu.Lock()
	defer s.tx.orderRows.mu.Unlock()

	existing, ok := s.tx.orderRows.rows[o.ID().String()]
	var next int
	if o.Version() == 0 {
		// 新規挿入。既に存在するなら衝突。
		if ok {
			return fmt.Errorf("注文 %q は既に存在します: %w", o.ID().String(), uow.ErrConcurrencyConflict)
		}
		next = 1
	} else {
		// 既存更新。存在しない、または版が食い違えば衝突。
		if !ok || existing.version != o.Version() {
			return fmt.Errorf("注文 %q のバージョンが一致しません: %w", o.ID().String(), uow.ErrConcurrencyConflict)
		}
		next = o.Version() + 1
	}

	s.tx.staged = append(s.tx.staged, orderToOrderRow(o, next))
	o.MarkPersisted(next)
	return nil
}

// NewReadOrderStore は読み取り用の OrderStore を返す。注文照会ユースケース用。
// 書き込みには使わない。
func NewReadOrderStore(orderRows *OrderRows) application.OrderStore {
	return &readOrderStore{orderRows: orderRows}
}

// readOrderStore は確定済みデータを直接読む読み取り専用アダプタ。
type readOrderStore struct {
	orderRows *OrderRows
}

var _ application.OrderStore = (*readOrderStore)(nil)

func (s *readOrderStore) Load(_ context.Context, id domain.OrderID) (*domain.Order, error) {
	return s.orderRows.load(id)
}

// Save は読み取り専用アダプタでは使用しない。誤用を早期に検知するためエラーを返す。
func (s *readOrderStore) Save(_ context.Context, _ *domain.Order) error {
	return fmt.Errorf("readOrderStore は読み取り専用です: 書き込みは UnitOfWork.Within を使ってください")
}
