package memory

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/example/go-ddd-template/contexts/ordering/internal/application"
	"github.com/example/go-ddd-template/contexts/ordering/internal/domain/order"
	"github.com/example/go-ddd-template/shared/outbox"
	"github.com/example/go-ddd-template/shared/uow"
)

// UnitOfWork はインメモリの擬似トランザクションによる作業単位。
// 注文ストアとアウトボックスを同一トランザクションに束ね、コミット時にまとめて確定させる。
//
// 任意で「同期配送シンク（sink）」を持てる（WithSyncDelivery）。設定されている場合、
// トランザクションのコミット成功直後に、そのトランザクションで積まれたメッセージを
// その場で（同期的に）シンクへ送出する。これは Docker/DB を使わない開発ハーネス
// （cmd/dev）向けの機構で、集約書き込みと同一トランザクションで積んだメッセージを、
// 別プロセスの送信中継（outbox.Runner）を介さずに決定的にピアへ届けるためのもの。
// 本番の耐障害な配送は outbox.Runner（ポーリング中継）が担う。
type UnitOfWork struct {
	store  *Store
	outbox *OutboxStore
	sink   outbox.Publisher
	log    *slog.Logger
}

// NewUnitOfWork はインメモリの作業単位を生成する。
func NewUnitOfWork(store *Store, outboxStore *OutboxStore) *UnitOfWork {
	return &UnitOfWork{store: store, outbox: outboxStore}
}

// WithSyncDelivery は開発用の同期配送シンクを設定する（本番では使わない）。設定すると、
// コミット成功直後に、そのトランザクションで積まれたメッセージを同期的に sink へ送出し、
// 送出できたものは送信済みとして記録する（背景の Runner が二重送出しないように）。
// これにより「集約の保存 → クロスコンテキストメッセージの配送」が 1 コールで決定的に完結する。
func (u *UnitOfWork) WithSyncDelivery(sink outbox.Publisher, log *slog.Logger) *UnitOfWork {
	u.sink = sink
	u.log = log
	return u
}

// コンパイル時にポートを満たしていることを確認する。
var _ application.UnitOfWork = (*UnitOfWork)(nil)

// Within は擬似トランザクションを開く。fn が成功すれば staging をコミットし、
// エラーを返せば staging を破棄（ロールバック）する。コミット成功後、同期配送シンクが
// 設定されていれば、そのトランザクションで積まれたメッセージをその場で送出する。
func (u *UnitOfWork) Within(ctx context.Context, fn func(ctx context.Context, r application.Repos) error) error {
	tx := &txState{store: u.store, outbox: u.outbox}
	r := repos{
		orders: &txStore{tx: tx},
		outbox: &txOutbox{tx: tx},
	}
	if err := fn(ctx, r); err != nil {
		return err // staging を破棄してロールバック
	}
	if err := tx.commit(); err != nil {
		return err
	}
	u.deliverSync(ctx, tx.stagedMsgs)
	return nil
}

// deliverSync は同期配送シンクが設定されている場合に、コミット済みメッセージをその場で
// 送出する。配送失敗はログに留め、コミット済みの操作は成功として扱う（本番では outbox.Runner
// が at-least-once で再送する。この開発用シンクにはその再送は無いことを明示する）。
func (u *UnitOfWork) deliverSync(ctx context.Context, msgs []outbox.Message) {
	if u.sink == nil || len(msgs) == 0 {
		return
	}
	for _, m := range msgs {
		if err := u.sink.Publish(ctx, m); err != nil {
			if u.log != nil {
				u.log.WarnContext(ctx, "同期配送に失敗しました（開発用シンクには再送がない点に注意）",
					slog.String("id", m.ID), slog.String("type", m.Type), slog.Any("error", err))
			}
			continue
		}
		_ = u.outbox.MarkPublished(ctx, m.ID)
	}
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
