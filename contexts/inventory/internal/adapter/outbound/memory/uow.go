package memory

import (
	"context"
	"fmt"

	"github.com/example/go-ddd-template/contexts/inventory/internal/application"
	"github.com/example/go-ddd-template/contexts/inventory/internal/domain/inventory"
	"github.com/example/go-ddd-template/shared/uow"
)

// UnitOfWork はインメモリの擬似トランザクションによる作業単位。
type UnitOfWork struct {
	store *Store
}

// NewUnitOfWork はインメモリの作業単位を生成する。
func NewUnitOfWork(store *Store) *UnitOfWork {
	return &UnitOfWork{store: store}
}

// コンパイル時にポートを満たしていることを確認する。
var _ application.UnitOfWork = (*UnitOfWork)(nil)

// Within は擬似トランザクションを開く。fn が成功すれば staging をコミットし、
// エラーを返せば staging を破棄（ロールバック）する。
// コミットは版チェックと適用を単一ロック下で原子的に行う。
func (u *UnitOfWork) Within(ctx context.Context, fn func(ctx context.Context, r application.Repos) error) error {
	tx := &txState{store: u.store}
	r := repos{stock: &txStore{tx: tx}}
	if err := fn(ctx, r); err != nil {
		return err // staging を破棄してロールバック
	}
	return tx.commit()
}

// repos は application.Repos の実装。
type repos struct {
	stock application.StockStore
}

func (r repos) Stock() application.StockStore { return r.stock }

// txState はトランザクション中の保存要求（staging）を蓄える。
// staged には「コミット時に確定ストアへ書き込む行」を貯める。
type txState struct {
	store  *Store
	staged []record
}

// commit は staging された行を確定ストアへ適用する。
// 版チェックと集約への版反映（MarkPersisted）は Save の時点で済ませているため、
// ここでは確定ストアへの書き込みだけを行う。ロールバック（fn がエラー）時は
// staged を破棄するだけでよく、確定ストアは変化しない。
func (tx *txState) commit() error {
	if len(tx.staged) == 0 {
		return nil
	}
	tx.store.mu.Lock()
	defer tx.store.mu.Unlock()
	for _, r := range tx.staged {
		tx.store.rows[r.sku] = r
	}
	return nil
}

// txStore はトランザクションに束ねた StockStore。
type txStore struct {
	tx *txState
}

// Load は確定済みデータから読み込む。
// 本スライスではユースケースが「読み込み → 保存」を 1 回ずつ行うため、
// 同一トランザクション内での read-your-writes（自分の書き込みの読み戻し）は実装しない。
func (s *txStore) Load(ctx context.Context, sku inventory.SKU) (*inventory.StockItem, error) {
	return s.tx.store.load(sku)
}

// Save は各集約の版を確定ストアと突き合わせて検証し、集約のバージョンを同期（MarkPersisted）
// したうえで、確定ストアへ書き込む行を staging に積む。実際の書き込みはコミット時に行う。
//
// 版チェックとバージョン反映を Save の時点で行うことで、ユースケースがクロージャ内で
// item.Version() を読んでも最新のバージョンが得られる（pgx アダプタと同じ観測契約になる）。
// 版が食い違えば uow.ErrConcurrencyConflict を返し、確定ストアは一切変更しない。
func (s *txStore) Save(ctx context.Context, items ...*inventory.StockItem) error {
	s.tx.store.mu.Lock()
	defer s.tx.store.mu.Unlock()

	for _, item := range items {
		existing, ok := s.tx.store.rows[item.SKU().String()]
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

		s.tx.staged = append(s.tx.staged, record{
			id:        item.ID(),
			sku:       item.SKU().String(),
			available: item.Available().Int(),
			version:   next,
		})
		item.MarkPersisted(next)
	}
	return nil
}

// NewReadStockStore は読み取り用の StockStore を返す。
// 在庫照会ユースケース用。書き込みには使わない。
func NewReadStockStore(store *Store) application.StockStore {
	return &readStore{store: store}
}

// readStore は確定済みデータを直接読む読み取り専用アダプタ。
type readStore struct {
	store *Store
}

func (s *readStore) Load(ctx context.Context, sku inventory.SKU) (*inventory.StockItem, error) {
	return s.store.load(sku)
}

// Save は読み取り専用アダプタでは使用しない。誤用を早期に検知するためエラーを返す。
func (s *readStore) Save(ctx context.Context, items ...*inventory.StockItem) error {
	return fmt.Errorf("readStore は読み取り専用です: 書き込みは UnitOfWork.Within を使ってください")
}
