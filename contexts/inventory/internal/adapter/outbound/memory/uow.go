package memory

import (
	"context"

	"github.com/example/go-ddd-template/contexts/inventory/internal/application"
	"github.com/example/go-ddd-template/shared/outbox"
)

// UnitOfWork はインメモリの擬似トランザクションによる作業単位。
// 在庫ストアと、配送キュー（一時的）・イベントログ（恒久記録）を束ねた Stores を
// 同一トランザクションに束ね、コミット時にまとめて確定させる。
type UnitOfWork struct {
	stockRows *StockRows
	stores    *Stores
}

// NewUnitOfWork はインメモリの作業単位を生成する。
// stores は配送キュー（送信後に削除される一時的なもの）と恒久イベントログ（追記のみ）を
// 束ねた backing store で、コミット時に CommitStaged で両方へ同時に確定される
// （PostgreSQL 構成で outbox と events を同一トランザクションに書くのと同じ意味論）。
func NewUnitOfWork(stockRows *StockRows, stores *Stores) *UnitOfWork {
	return &UnitOfWork{stockRows: stockRows, stores: stores}
}

// コンパイル時にポートを満たしていることを確認する。
var _ application.UnitOfWork = (*UnitOfWork)(nil)

// Within は擬似トランザクションを開く。fn が成功すれば staging をコミットし、
// エラーを返せば staging を破棄（ロールバック）する。
func (u *UnitOfWork) Within(ctx context.Context, fn func(ctx context.Context, r application.Repos) error) error {
	// 結線。集約を 1 つ足すときに変わるのはここだけで、rows.go の共通機構と
	// txState には差分が出ない。
	stock := &staging[stockRow]{target: u.stockRows}
	tx := &txState{stores: u.stores, groups: []committer{stock}}
	r := repos{
		stock:  &stockStore{rows: u.stockRows, staging: stock},
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
// 版チェックと集約への版反映（MarkPersisted）は Save の時点で済ませているため、
// ここでは確定ストアへの書き込みだけを行う。ロールバック（fn がエラー）時は staging を
// 破棄するだけでよく、確定ストアは変化しない。
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
