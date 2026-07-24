package memory

import (
	"context"
	"fmt"
	"time"

	"github.com/example/go-ddd-template/contexts/inventory/internal/application"
	"github.com/example/go-ddd-template/contexts/inventory/internal/domain/inventory"
	"github.com/example/go-ddd-template/shared/outbox"
	"github.com/example/go-ddd-template/shared/uow"
)

// UnitOfWork はインメモリの擬似トランザクションによる作業単位。
// 在庫ストア・アウトボックス（一時的な配送キュー）・イベントログ（恒久記録）を
// 同一トランザクションに束ね、コミット時にまとめて確定させる。
type UnitOfWork struct {
	store  *Store
	outbox *OutboxStore
	events *EventStore
}

// NewUnitOfWork はインメモリの作業単位を生成する。
// eventsStore は発行メッセージの恒久ログで、outboxStore と同じコミットで追記される
// （PostgreSQL 構成で outbox と events を同一トランザクションに書くのと同じ意味論）。
func NewUnitOfWork(store *Store, outboxStore *OutboxStore, eventsStore *EventStore) *UnitOfWork {
	return &UnitOfWork{store: store, outbox: outboxStore, events: eventsStore}
}

// コンパイル時にポートを満たしていることを確認する。
var _ application.UnitOfWork = (*UnitOfWork)(nil)

// Within は擬似トランザクションを開く。fn が成功すれば staging をコミットし、
// エラーを返せば staging を破棄（ロールバック）する。
func (u *UnitOfWork) Within(ctx context.Context, fn func(ctx context.Context, r application.Repos) error) error {
	tx := &txState{store: u.store, outbox: u.outbox, events: u.events}
	r := repos{
		stock:  &txStore{tx: tx},
		outbox: &txOutbox{tx: tx},
	}
	if err := fn(ctx, r); err != nil {
		return err // staging を破棄してロールバック
	}
	return tx.commit()
}

// repos は application.Repos の実装。
type repos struct {
	stock  application.StockStore
	outbox application.MessagePublisher
}

func (r repos) Stock() application.StockStore        { return r.stock }
func (r repos) Outbox() application.MessagePublisher { return r.outbox }

// txState はトランザクション中の保存要求（staging）を蓄える。
type txState struct {
	store      *Store
	outbox     *OutboxStore
	events     *EventStore
	staged     []record
	stagedMsgs []outbox.Message
}

// commit は staging された在庫行とメッセージを確定ストアへ適用する。
// 版チェックと集約への版反映（MarkPersisted）は Save の時点で済ませているため、
// ここでは確定ストアへの書き込みだけを行う。ロールバック（fn がエラー）時は staged を
// 破棄するだけでよく、確定ストアは変化しない。
//
// メッセージは配送キュー（outbox）と恒久イベントログ（events）の両方へ同じコミットで
// 積む。PostgreSQL 構成で Enqueue が同一トランザクションに両表を書くのと同じ意味論で、
// 片方だけ残ることはない。
func (tx *txState) commit() error {
	if len(tx.staged) > 0 {
		tx.store.mu.Lock()
		for _, r := range tx.staged {
			tx.store.rows[r.sku] = r
		}
		tx.store.mu.Unlock()
	}
	tx.outbox.appendCommitted(tx.stagedMsgs)
	tx.events.appendCommitted(tx.stagedMsgs)
	return nil
}

// txStore はトランザクションに束ねた StockStore。
type txStore struct {
	tx *txState
}

// コンパイル時にポートを満たしていることを確認する。
var _ application.StockStore = (*txStore)(nil)

func (s *txStore) Load(_ context.Context, sku inventory.SKU) (*inventory.StockItem, error) {
	return s.tx.store.load(sku)
}

func (s *txStore) LoadMany(_ context.Context, skus []inventory.SKU) ([]*inventory.StockItem, error) {
	return s.tx.store.loadMany(skus)
}

func (s *txStore) LoadByReservation(_ context.Context, ref inventory.ReservationRef) ([]*inventory.StockItem, error) {
	return s.tx.store.loadByReservation(ref)
}

func (s *txStore) LoadExpiredPending(_ context.Context, before time.Time, limit int) ([]*inventory.StockItem, error) {
	return s.tx.store.loadExpiredPending(before, limit)
}

// Save は各集約の版を確定ストアと突き合わせて検証し、集約のバージョンを同期（MarkPersisted）
// したうえで、確定ストアへ書き込む行（予約状態を含む）を staging に積む。実際の書き込みは
// コミット時に行う。版が食い違えば uow.ErrConcurrencyConflict を返し、確定ストアは変更しない。
func (s *txStore) Save(_ context.Context, items ...*inventory.StockItem) error {
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

		s.tx.staged = append(s.tx.staged, itemToRecord(item, next))
		item.MarkPersisted(next)
	}
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

// NewReadStockStore は読み取り用の StockStore を返す。
// 在庫照会ユースケース用。書き込みには使わない。
func NewReadStockStore(store *Store) application.StockStore {
	return &readStore{store: store}
}

// readStore は確定済みデータを直接読む読み取り専用アダプタ。
type readStore struct {
	store *Store
}

var _ application.StockStore = (*readStore)(nil)

func (s *readStore) Load(_ context.Context, sku inventory.SKU) (*inventory.StockItem, error) {
	return s.store.load(sku)
}

func (s *readStore) LoadMany(_ context.Context, skus []inventory.SKU) ([]*inventory.StockItem, error) {
	return s.store.loadMany(skus)
}

func (s *readStore) LoadByReservation(_ context.Context, ref inventory.ReservationRef) ([]*inventory.StockItem, error) {
	return s.store.loadByReservation(ref)
}

func (s *readStore) LoadExpiredPending(_ context.Context, before time.Time, limit int) ([]*inventory.StockItem, error) {
	return s.store.loadExpiredPending(before, limit)
}

// Save は読み取り専用アダプタでは使用しない。誤用を早期に検知するためエラーを返す。
func (s *readStore) Save(_ context.Context, _ ...*inventory.StockItem) error {
	return fmt.Errorf("readStore は読み取り専用です: 書き込みは UnitOfWork.Within を使ってください")
}
