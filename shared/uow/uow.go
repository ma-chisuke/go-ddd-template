// Package uow は Unit of Work（作業単位）パターンを提供する。
//
// このパッケージの中心的な設計判断は「トランザクション境界を明示的に所有する」ことである。
// トランザクションハンドルを context.Context に隠して引き回すアンチパターンは採らない。
// 代わりに、UnitOfWork.Within がトランザクションを開始し、そのトランザクションに束ねた
// リポジトリの束（型パラメータ R）をコールバックへ引き渡す。ユースケースはこの R からしか
// リポジトリを取得できないため、「トランザクションの外で書き込みが走る」ことが構造的に
// 起こり得ない。
//
// 楽観的排他制御（optimistic concurrency control）による衝突は Executor と Run が扱う。
// Run はコマンドを実行し、ErrConcurrencyConflict を検知したら指数バックオフを挟んで
// 規定回数まで再試行する。再試行のたびに Within が新しいトランザクションを開くため、
// コマンドが内部で「読み込み → ドメイン操作 → 保存」を行っていれば、再試行時には
// 最新状態を読み直したうえでやり直される。
package uow

import (
	"context"
	"errors"
	"time"
)

// ErrConcurrencyConflict は楽観的排他制御で版（version）の不一致が起きたことを表す、
// 永続化レイヤのセンチネルエラーである。ドメインのエラーではない点に注意すること。
// リポジトリの Save 実装は、期待した版と保存先の版が食い違ったときにこれを返す。
var ErrConcurrencyConflict = errors.New("concurrency conflict")

// UnitOfWork は、型パラメータ R（コンテキストごとのリポジトリ束）に束ねた
// トランザクションを提供する。Within はトランザクションを開き、そのトランザクションに
// 束ねた R を組み立てて fn に渡し、fn が nil を返せばコミット、エラーを返せば
// ロールバックする。トランザクション境界の所有者は Within である。
type UnitOfWork[R any] interface {
	Within(ctx context.Context, fn func(ctx context.Context, repos R) error) error
}

// Executor は Run の再試行ポリシー（最大試行回数とバックオフ）を保持する不変の設定値。
// 生成には NewExecutor を用いる。ゼロ値は使わないこと。
type Executor struct {
	// maxAttempts は最初の 1 回を含む最大試行回数。
	maxAttempts int
	// baseBackoff は指数バックオフの基準待機時間。
	baseBackoff time.Duration
}

// Option は NewExecutor の設定を変更する関数オプション。
type Option func(*Executor)

// WithMaxAttempts は最大試行回数を設定する（最初の 1 回を含む）。1 未満は 1 に丸める。
func WithMaxAttempts(n int) Option {
	return func(e *Executor) {
		if n < 1 {
			n = 1
		}
		e.maxAttempts = n
	}
}

// WithBaseBackoff は指数バックオフの基準待機時間を設定する。0 以下は無効化（待機なし）扱い。
func WithBaseBackoff(d time.Duration) Option {
	return func(e *Executor) { e.baseBackoff = d }
}

// NewExecutor は再試行ポリシーを組み立てる。既定は最大 3 回・基準 5ms の指数バックオフ。
func NewExecutor(opts ...Option) Executor {
	e := Executor{
		maxAttempts: 3,
		baseBackoff: 5 * time.Millisecond,
	}
	for _, opt := range opts {
		opt(&e)
	}
	return e
}

// Run はコマンド cmd を UnitOfWork のトランザクション内で実行し、
// 楽観的排他制御の衝突（ErrConcurrencyConflict）に対して指数バックオフで再試行する。
//
// Go のメソッドは型パラメータを持てないため、Run は Executor のメソッドではなく
// 自由関数として定義している。呼び出し側は cmd の中で repos（型 R）からのみ
// リポジトリを取得すること。こうすることで、トランザクション外の書き込みを
// コンパイル時に防げる。
//
// cmd は「読み込み → ドメイン操作 → 保存」を Within の内側で完結させること。
// そうすれば再試行時に最新状態を読み直したうえでやり直される。
func Run[R any](ctx context.Context, x Executor, unit UnitOfWork[R], cmd func(context.Context, R) error) error {
	var lastErr error
	for attempt := 1; attempt <= x.maxAttempts; attempt++ {
		err := unit.Within(ctx, func(ctx context.Context, repos R) error {
			return cmd(ctx, repos)
		})
		if err == nil {
			return nil
		}
		// 衝突以外のエラーは再試行しても解消しないため即座に返す。
		if !errors.Is(err, ErrConcurrencyConflict) {
			return err
		}
		lastErr = err
		// 最終試行なら待機せずに抜ける。
		if attempt == x.maxAttempts {
			break
		}
		if err := sleepBackoff(ctx, x.baseBackoff, attempt); err != nil {
			// context がキャンセルされた場合はそれを返す。
			return err
		}
	}
	return lastErr
}

// sleepBackoff は指数バックオフ（base * 2^(attempt-1)）だけ待機する。
// 待機中に context がキャンセルされたら、その理由を返す。
func sleepBackoff(ctx context.Context, base time.Duration, attempt int) error {
	if base <= 0 {
		return nil
	}
	d := base << (attempt - 1)
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
