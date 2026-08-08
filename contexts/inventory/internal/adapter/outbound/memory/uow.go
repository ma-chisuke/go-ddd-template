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
	tx := &txState{stockRows: u.stockRows, stores: u.stores}
	r := repos{
		stock:  &stockStore{tx: tx},
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
	stockRows  *StockRows
	stores     *Stores
	staged     []stockRow
	stagedMsgs []outbox.Message
}

// commit は staging された在庫行とメッセージを確定ストアへ適用する。
// 版チェックと集約への版反映（MarkPersisted）は Save の時点で済ませているため、
// ここでは確定ストアへの書き込みだけを行う。ロールバック（fn がエラー）時は staged を
// 破棄するだけでよく、確定ストアは変化しない。
//
// メッセージは Stores.CommitStaged 一発で配送キューと恒久イベントログの両方へ同じコミットで
// 積む。PostgreSQL 構成で Enqueue が同一トランザクションに両表を書くのと同じ意味論で、
// 片方だけ残る状態は型に存在しない。
func (tx *txState) commit() error {
	if len(tx.staged) > 0 {
		tx.stockRows.mu.Lock()
		for _, r := range tx.staged {
			tx.stockRows.rows[r.sku] = r
		}
		tx.stockRows.mu.Unlock()
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
