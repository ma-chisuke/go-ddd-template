package memory

import (
	"context"
	"fmt"

	"github.com/example/go-ddd-template/contexts/ordering/internal/application"
	"github.com/example/go-ddd-template/contexts/ordering/internal/domain"
	"github.com/example/go-ddd-template/shared/uow"
)

// orderStore はトランザクションに束ねた OrderStore。
// 読み取りは確定済みの行を見る（同一トランザクションで staging した行は見えない）。
type orderStore struct {
	rows    *OrderRows
	staging *staging[orderRow]
}

// コンパイル時にポートを満たしていることを確認する。
var _ application.OrderStore = (*orderStore)(nil)

func (s *orderStore) Load(_ context.Context, id domain.OrderID) (*domain.Order, error) {
	return loadOrder(s.rows, id)
}

// Save は集約の版を確定済みの行と突き合わせて検証し、集約のバージョンを同期
// （MarkPersisted）したうえで、書き込む行を staging に積む。実際の書き込みはコミット時に
// 行う。版が食い違えば uow.ErrConcurrencyConflict を返し、確定済みの行は変更しない。
//
// 版の読み取りと staging への積み込みを withLock で不可分に行う。get で読んでから積む形へ
// 崩すと、その間に他のトランザクションが割り込んで同じ版を 2 つのトランザクションが
// 取得できてしまう。
func (s *orderStore) Save(_ context.Context, o *domain.Order) error {
	return s.rows.withLock(func(m map[string]orderRow) error {
		existing, ok := m[o.ID().String()]
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

		s.staging.stage(o.ID().String(), orderToOrderRow(o, next))
		o.MarkPersisted(next)
		return nil
	})
}

// NewReadOrderStore は読み取り用の OrderStore を返す。注文照会ユースケース用。
// 書き込みには使わない。
func NewReadOrderStore(orderRows *OrderRows) application.OrderStore {
	return &readOrderStore{rows: orderRows}
}

// readOrderStore は確定済みデータを直接読む読み取り専用アダプタ。
type readOrderStore struct {
	rows *OrderRows
}

var _ application.OrderStore = (*readOrderStore)(nil)

func (s *readOrderStore) Load(_ context.Context, id domain.OrderID) (*domain.Order, error) {
	return loadOrder(s.rows, id)
}

// Save は読み取り専用アダプタでは使用しない。誤用を早期に検知するためエラーを返す。
func (s *readOrderStore) Save(_ context.Context, _ *domain.Order) error {
	return fmt.Errorf("readOrderStore は読み取り専用です: 書き込みは UnitOfWork.Within を使ってください")
}

// loadOrder は確定済みの行から注文集約を復元する。共通機構 rows[R] は orderRow の中身を
// 知らないため、この集約固有の読み取りは backing store ではなくストア側に置く。
func loadOrder(orderRows *OrderRows, id domain.OrderID) (*domain.Order, error) {
	r, ok := orderRows.get(id.String())
	if !ok {
		return nil, fmt.Errorf("注文 %q: %w", id.String(), domain.ErrOrderNotFound)
	}
	return orderRowToOrder(r)
}
