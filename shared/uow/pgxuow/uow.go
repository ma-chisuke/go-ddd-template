// Package pgxuow は、作業単位（Unit of Work）の pgx ドライバ実装を提供する。
//
// 純粋な shared/uow パッケージはトランザクション境界の「契約」（UnitOfWork[R]
// インターフェース）と楽観的排他制御の再試行機構（Run/Executor）だけを持ち、
// 特定のデータベースドライバに依存しない。本パッケージはその契約を pgxpool
// で具体化したもので、Begin/Commit/Rollback といった pgx 固有のトランザクション・
// ライフサイクルをここ 1 箇所に集約する。各コンテキストは「そのトランザクションに
// どのリポジトリ束（R）を組み立てるか」を表す buildRepos クロージャだけを供給する。
//
// これにより application 層は純粋な shared/uow のみを import すればよく、pgx を
// 直接にも推移的にも引き込まない（ポート/アダプタ分離の維持）。pgx を import して
// よいのはこのパッケージと各コンテキストの outbound アダプタ環だけである。
//
// ディレクトリ名 = パッケージ名 = pgxuow である（dir=package）。パッケージ名を
// pgx にしないのは、呼び出し側が pgx.Tx のために import する
// github.com/jackc/pgx/v5（パッケージ名 pgx）と識別子が衝突するためである。
// pgxuow とすることで衝突を避けつつ、import 側は別名エイリアス不要で
//
//	import "github.com/example/go-ddd-template/shared/uow/pgxuow"
//	uow := pgxuow.New(pool, buildRepos)
//
// と書ける。将来 database/sql 版を足すなら shared/uow/sqluow のように、同じ傘の下へ
// 「<driver>uow」という名前で並べる。
package pgxuow

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// UnitOfWork は pgxpool を用いた実トランザクションの作業単位。
// 型パラメータ R はコンテキストごとのリポジトリ束（application.Repos）で、
// buildRepos がトランザクションに束ねた R を組み立てる。トランザクション境界の
// 所有者は Within であり、shared/uow.UnitOfWork[R] を満たす（型充足は呼び出し側
// コンテキストの型エイリアス経由で具象 R とともにコンパイル時に確認される）。
type UnitOfWork[R any] struct {
	pool       *pgxpool.Pool
	buildRepos func(pgx.Tx) R
}

// New は pgxpool と「tx から Repos 束を作る関数」から汎用の作業単位を生成する。
// buildRepos はトランザクションごとに 1 回呼ばれ、そのトランザクションに束ねた
// リポジトリの束 R を返す（例: q := sqlcgen.New(tx); return repos{...}）。
func New[R any](pool *pgxpool.Pool, buildRepos func(tx pgx.Tx) R) *UnitOfWork[R] {
	return &UnitOfWork[R]{pool: pool, buildRepos: buildRepos}
}

// Within はトランザクションを開始し、fn が nil を返せばコミット、
// エラーを返せばロールバックする。トランザクションに束ねた R を buildRepos で
// 組み立てて fn へ渡すため、fn 内の書き込みはすべて同一トランザクションに参加し、
// 原子的にコミットされる。
func (u *UnitOfWork[R]) Within(ctx context.Context, fn func(ctx context.Context, repos R) error) error {
	tx, err := u.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("トランザクションの開始に失敗しました: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			// コミット済みなら Rollback は no-op。念のため常に呼ぶ。
			_ = tx.Rollback(ctx)
		}
	}()

	// トランザクションに束ねた Repos を組み立てる。
	// どのリポジトリを束ねるか（在庫ストア/注文ストア + アウトボックス）は
	// コンテキストが供給する buildRepos クロージャが決める。
	r := u.buildRepos(tx)
	if err := fn(ctx, r); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("コミットに失敗しました: %w", err)
	}
	committed = true
	return nil
}
