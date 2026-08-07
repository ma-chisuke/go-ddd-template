package memory

import (
	"context"
	"sync"

	"github.com/example/go-ddd-template/contexts/inventory/internal/application"
	"github.com/example/go-ddd-template/shared/outbox"
)

// UnitOfWork はインメモリの擬似トランザクションによる作業単位。
// 在庫ストアと、配送キュー（一時的）・イベントログ（恒久記録）を束ねた Stores を
// 同一トランザクションに束ね、コミット時にまとめて確定させる。
type UnitOfWork struct {
	rows   *StockItemRows
	stores *Stores
}

// NewUnitOfWork はインメモリの作業単位を生成する。
// stores は配送キュー（送信後に削除される一時的なもの）と恒久イベントログ（追記のみ）を
// 束ねた backing store で、コミット時に CommitStaged で両方へ同時に確定される
// （PostgreSQL 構成で outbox と events を同一トランザクションに書くのと同じ意味論）。
func NewUnitOfWork(rows *StockItemRows, stores *Stores) *UnitOfWork {
	return &UnitOfWork{rows: rows, stores: stores}
}

// コンパイル時にポートを満たしていることを確認する。
var _ application.UnitOfWork = (*UnitOfWork)(nil)

// Within は擬似トランザクションを開く。fn が成功すれば staging をコミットし、
// エラーを返せば staging を破棄（ロールバック）する。
func (u *UnitOfWork) Within(ctx context.Context, fn func(ctx context.Context, r application.Repos) error) error {
	tx := &txState{stores: u.stores}
	r := repos{
		stock:  &txStockStore{tx: tx, rows: u.rows},
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

// applyGroup は「ひとつの backing store に対する書き込みの束」。
//
// writes の各クロージャは **lock を取得済みの状態で** 呼ばれる（クロージャ自身はロックを
// 取らない）。これにより、同じ backing store への複数行の書き込みが 1 回のロックで不可分に
// 確定する。マルチ SKU 予約（StockStore.Save に複数の StockItem を渡す経路）が直撃点で、
// 行ごとにロックを取り直す形にすると束の途中の状態が他の goroutine から観測されうる。
type applyGroup struct {
	lock   sync.Locker
	writes []func()
}

// txState はトランザクション中の保存要求（staging）を蓄える。
//
// **集約の種類を知らない。** 集約ごとにスライスを並べるのではなく「確定時に実行する操作の列」
// として持つため、集約ルートを 1 つ足しても下の commit は 1 文字も変わらない。
type txState struct {
	groups     []*applyGroup
	stores     *Stores
	stagedMsgs []outbox.Message
}

// stage は backing store に対する書き込みを積む。同じ store（= 同じ lock）への 2 回目以降は
// 既存のグループへ追加され、ロックはコミット時に 1 回だけ取られる。
//
// 版の検証は呼び出し側（tx<集約名>Store.Save）が済ませてから積む。ここに積まれた時点で
// 「あとは書くだけ」であり、ロールバックは単に実行しないことで成立する。
func (tx *txState) stage(lock sync.Locker, write func()) {
	for _, g := range tx.groups {
		if g.lock == lock {
			g.writes = append(g.writes, write)
			return
		}
	}
	tx.groups = append(tx.groups, &applyGroup{lock: lock, writes: []func(){write}})
}

// commit は staging された書き込みとメッセージを確定ストアへ適用する。
// 版チェックと集約への版反映（MarkPersisted）は Save の時点で済ませているため、
// ここでは確定ストアへの書き込みだけを行う。ロールバック（fn がエラー）時は groups を
// 破棄するだけでよく、確定ストアは変化しない。
//
// backing store ごとに 1 回だけロックを取り、その内側でその store 宛の書き込みをすべて
// 実行する。グループは登録順に並ぶのでロック順序は決定的であり、デッドロックしない。
//
// **保証する範囲**: ひとつの backing store に対する複数行の書き込みは不可分である
// （マルチ SKU 予約が該当する）。
// **保証しない範囲**: 異なる backing store を跨ぐ不可分性。ユースケースはトランザクション内で
// 1 種類の集約しか書かないので、この状況は発生しない。
//
// メッセージは Stores.CommitStaged 一発で配送キューと恒久イベントログの両方へ同じコミットで
// 積む。PostgreSQL 構成で Enqueue が同一トランザクションに両表を書くのと同じ意味論で、
// 片方だけ残る状態は型に存在しない。
func (tx *txState) commit() error {
	for _, g := range tx.groups {
		g.lock.Lock()
		for _, w := range g.writes {
			w()
		}
		g.lock.Unlock()
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
