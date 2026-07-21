package memory

import (
	"context"
	"fmt"

	"github.com/example/go-ddd-template/contexts/ordering/internal/application"
	"github.com/example/go-ddd-template/contexts/ordering/internal/domain/order"
	"github.com/example/go-ddd-template/shared/outbox"
	"github.com/example/go-ddd-template/shared/uow"
)

// UnitOfWork はインメモリの擬似トランザクションによる作業単位。
// 注文ストアとアウトボックスを同一トランザクションに束ね、コミット時にまとめて確定させる。
type UnitOfWork struct {
	store  *Store
	outbox *OutboxStore
}

// NewUnitOfWork はインメモリの作業単位を生成する。
func NewUnitOfWork(store *Store, outboxStore *OutboxStore) *UnitOfWork {
	return &UnitOfWork{store: store, outbox: outboxStore}
}

// コンパイル時にポートを満たしていることを確認する。
var _ application.UnitOfWork = (*UnitOfWork)(nil)

// Within は擬似トランザクションを開く。fn が成功すれば staging をコミットし、
// エラーを返せば staging を破棄（ロールバック）する。
func (u *UnitOfWork) Within(ctx context.Context, fn func(ctx context.Context, r application.Repos) error) error {
	tx := &txState{store: u.store, outbox: u.outbox}
	r := repos{
		orders: &txStore{tx: tx},
		outbox: &txOutbox{tx: tx},
	}
	if err := fn(ctx, r); err != nil {
		return err // staging を破棄してロールバック
	}
	return tx.commit()
}

// repos は application.Repos の実装。
type repos struct {
	orders application.OrderStore
	outbox application.MessagePublisher
}

func (r repos) Orders() application.OrderStore       { return r.orders }
func (r repos) Outbox() application.MessagePublisher { return r.outbox }

// txState はトランザクション中の保存要求（staging）を蓄える。
type txState struct {
	store      *Store
	outbox     *OutboxStore
	staged     []record
	stagedMsgs []outbox.Message
}

// commit は staging された注文行とアウトボックスメッセージを確定ストアへ適用する。
func (tx *txState) commit() error {
	if len(tx.staged) > 0 {
		tx.store.mu.Lock()
		for _, r := range tx.staged {
			tx.store.rows[r.id] = r
		}
		tx.store.mu.Unlock()
	}
	tx.outbox.appendCommitted(tx.stagedMsgs)
	return nil
}

// txStore はトランザクションに束ねた OrderStore。
type txStore struct {
	tx *txState
}

// コンパイル時にポートを満たしていることを確認する。
var _ application.OrderStore = (*txStore)(nil)

func (s *txStore) Load(_ context.Context, id order.OrderID) (*order.Order, error) {
	return s.tx.store.load(id)
}

// Save は集約の版を確定ストアと突き合わせて検証し、集約のバージョンを同期（MarkPersisted）
// したうえで、確定ストアへ書き込む行を staging に積む。実際の書き込みはコミット時に行う。
// 版が食い違えば uow.ErrConcurrencyConflict を返し、確定ストアは変更しない。
func (s *txStore) Save(_ context.Context, o *order.Order) error {
	s.tx.store.mu.Lock()
	defer s.tx.store.mu.Unlock()

	existing, ok := s.tx.store.rows[o.ID().String()]
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

	s.tx.staged = append(s.tx.staged, orderToRecord(o, next))
	o.MarkPersisted(next)
	return nil
}

// txOutbox はトランザクションに束ねた MessagePublisher。Enqueue はコミット時に確定する。
type txOutbox struct {
	tx *txState
}

// コンパイル時にポートを満たしていることを確認する。
var _ application.MessagePublisher = (*txOutbox)(nil)

// Enqueue はメッセージを staging に積む。実際の確定はコミット時に行うため、集約の保存と
// 同一トランザクションで原子的にコミットされる（二重書き込みを避ける）。
func (o *txOutbox) Enqueue(_ context.Context, m outbox.Message) error {
	o.tx.stagedMsgs = append(o.tx.stagedMsgs, m)
	return nil
}

// NewReadOrderStore は読み取り用の OrderStore を返す。注文照会ユースケース用。
// 書き込みには使わない。
func NewReadOrderStore(store *Store) application.OrderStore {
	return &readStore{store: store}
}

// readStore は確定済みデータを直接読む読み取り専用アダプタ。
type readStore struct {
	store *Store
}

var _ application.OrderStore = (*readStore)(nil)

func (s *readStore) Load(_ context.Context, id order.OrderID) (*order.Order, error) {
	return s.store.load(id)
}

// Save は読み取り専用アダプタでは使用しない。誤用を早期に検知するためエラーを返す。
func (s *readStore) Save(_ context.Context, _ *order.Order) error {
	return fmt.Errorf("readStore は読み取り専用です: 書き込みは UnitOfWork.Within を使ってください")
}
