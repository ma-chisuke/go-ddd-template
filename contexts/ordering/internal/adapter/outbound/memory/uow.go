package memory

import (
	"context"
	"log/slog"

	"github.com/example/go-ddd-template/contexts/ordering/internal/application"
	"github.com/example/go-ddd-template/shared/outbox"
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
	orderRows    *OrderRows
	shipmentRows *ShipmentRows
	stores       *Stores
	sink         outbox.Publisher
	log          *slog.Logger
}

// NewUnitOfWork はインメモリの作業単位を生成する。
// stores は配送キュー（送信後に削除される一時的なもの）と恒久イベントログ（追記のみ）を
// 束ねた backing store で、コミット時に CommitStaged で両方へ同時に確定される
// （PostgreSQL 構成で outbox と events を同一トランザクションに書くのと同じ意味論）。
func NewUnitOfWork(orderRows *OrderRows, shipmentRows *ShipmentRows, stores *Stores) *UnitOfWork {
	return &UnitOfWork{orderRows: orderRows, shipmentRows: shipmentRows, stores: stores}
}

// WithSyncDelivery は開発用の同期配送シンクを設定する（本番では使わない）。設定すると、
// コミット成功直後に、そのトランザクションで積まれたメッセージを同期的に sink へ送出し、
// 送出できたものは配送キューから取り除く（背景の Runner が二重送出しないように）。
// 恒久イベントログ（events）には残るため、削除しても発行の記録は失われない。
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
	// 結線。集約を 1 つ足すときに変わるのはここだけで、rows.go の共通機構と
	// txState には差分が出ない。
	orders := &staging[orderRow]{target: u.orderRows}
	shipments := &staging[shipmentRow]{target: u.shipmentRows}
	tx := &txState{stores: u.stores, groups: []committer{orders, shipments}}
	r := repos{
		orders:    &orderStore{rows: u.orderRows, staging: orders},
		shipments: &shipmentStore{rows: u.shipmentRows, staging: shipments},
		outbox:    &txOutbox{tx: tx},
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
//
// Runner と同じく「送出に成功してから配送キューから消す」順序を守るため、送出に失敗した
// メッセージはキューに残る（前進性は delete 意味論でも変わらない）。
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
		_ = u.stores.Outbox().MarkPublished(ctx, m.ID)
	}
}

// repos は application.Repos の実装。
type repos struct {
	orders    application.OrderStore
	shipments application.ShipmentStore
	outbox    application.MessagePublisher
}

func (r repos) Orders() application.OrderStore       { return r.orders }
func (r repos) Shipments() application.ShipmentStore { return r.shipments }
func (r repos) Outbox() application.MessagePublisher { return r.outbox }

// committer は「このトランザクションで自分に溜まった書き込みを確定する」もの。
// staging[R] が R によらず満たす。txState はこれ以上のことを知らない。
type committer interface{ commit() }

// txState はトランザクション中の保存要求（staging）を蓄える。
//
// 集約の種類を知らない。集約ごとの staging は groups に committer として登録され、
// txState はそれを順に確定するだけである。集約ルートを 1 つ足しても本型と commit は変わらない。
type txState struct {
	stores     *Stores
	stagedMsgs []outbox.Message
	groups     []committer // 登録順に確定する
}

// commit は各 staging とメッセージを確定ストアへ適用する。
//
// メッセージは Stores.CommitStaged 一発で配送キューと恒久イベントログの両方へ同じコミットで
// 積む。PostgreSQL 構成で Enqueue が同一トランザクションに両表を書くのと同じ意味論で、
// 片方だけ残る状態は型に存在しない。
//
// 保証するのは「同一 backing store への複数行が不可分に確定する」ところまでで、
// backing store を跨ぐ不可分性は保証しない（groups を順に確定する間、他のゴルーチンは
// 中間状態を観測しうる）。1 つのトランザクションで書き込む集約ルートは 1 つに保つため、
// 複数の groups が非空になる経路は無い。これはインメモリ擬似トランザクションの既知の限界で、
// PostgreSQL 構成では実トランザクションが跨ぐ不可分性も保証する。
func (tx *txState) commit() error {
	for _, g := range tx.groups {
		g.commit()
	}
	tx.stores.CommitStaged(tx.stagedMsgs)
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
