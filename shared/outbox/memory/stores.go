// Package memory はアウトボックス機構のインメモリ backing store を提供する。
//
// 配送キュー（一時的な outbox）と恒久イベントログ（events）を 1 つの Stores に束ね、
// 両者への書き込みを「必ず同時に・同一ロックの下で」行う CommitStaged だけを書き込みの
// 継ぎ目として公開する。片方だけを書く API は型に存在しないため、「キューに積んだが
// ログに無い」状態は構造的に起きえない（規約ではなく型で保証される）。
//
// これはテスト用のモックではなく、PostgreSQL アダプタと同じ意味論（outbox は
// delete-after-publish、events は追記専用）を DB 非依存で再現する「本物の backing store」で
// ある。ドメインにもコンテキスト固有コードにも依存しないため、どの境界づけられた
// コンテキストの memory UnitOfWork からでも共有できる。
package memory

import (
	"context"
	"sync"

	"github.com/example/go-ddd-template/shared/outbox"
)

// Stores は配送キュー（outbox）と恒久イベントログ（events）を束ねたインメモリ backing store。
// 両者へ「必ず同時に」書く CommitStaged だけが書き込みの継ぎ目であり、片方だけを書く API は
// 公開されない。キューとログへの追記・削除は同一の mutex の下で行うため、中間状態を
// 他の goroutine から観測できない。
type Stores struct {
	mu    sync.Mutex
	queue []outbox.Message // 未送信のみ（送信成功後に削除される一時的な配送キュー）
	log   []outbox.Message // 追記専用・削除しない恒久イベントログ
}

// NewStores は空のキューと空のログを持つ Stores を生成する。
func NewStores() *Stores {
	return &Stores{}
}

// CommitStaged は UnitOfWork のコミット時に、そのトランザクションでステージされた
// メッセージを配送キューと恒久ログの両方へ「1 回のロックの下で」追記する。
// 片方だけが成功する状態は表現できない（R-3）。
func (s *Stores) CommitStaged(msgs []outbox.Message) {
	if len(msgs) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queue = append(s.queue, msgs...)
	s.log = append(s.log, msgs...)
}

// Outbox は送信中継（outbox.Runner）や同期配送シンクへ渡す MessageStore ビューを返す。
// このビューを通じた Enqueue はキューと恒久ログの両方へ書き（PostgreSQL と同じ意味論）、
// Unpublished / MarkPublished は配送キューのみを触る。
func (s *Stores) Outbox() outbox.MessageStore {
	return outboxView{s: s}
}

// Events は恒久ログの写しを発生順に返す（検証用・読み出しのみ）。ログは削除されない（R-4）。
// 呼び出し側が返り値を書き換えても内部状態は壊れない。
func (s *Stores) Events() []outbox.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]outbox.Message, len(s.log))
	copy(out, s.log)
	return out
}

// Queued は配送キューに残っている（＝未送信の）メッセージの写しを返す（検証用）。
func (s *Stores) Queued() []outbox.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]outbox.Message, len(s.queue))
	copy(out, s.queue)
	return out
}

// enqueue はキューと恒久ログの両方へ 1 件を追記する（Outbox().Enqueue の実体）。
func (s *Stores) enqueue(m outbox.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queue = append(s.queue, m)
	s.log = append(s.log, m)
}

// unpublished は配送キューの未送信メッセージを最大 limit 件返す（読み出しのみ）。
func (s *Stores) unpublished(limit int) []outbox.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []outbox.Message
	for _, m := range s.queue {
		out = append(out, m)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

// markPublished は送信に成功したメッセージを配送キューから取り除く（delete-after-publish）。
// 恒久ログには影響しない（R-2 / R-4）。
func (s *Stores) markPublished(ids ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	drop := make(map[string]bool, len(ids))
	for _, id := range ids {
		drop[id] = true
	}
	kept := make([]outbox.Message, 0, len(s.queue))
	for _, m := range s.queue {
		if drop[m.ID] {
			continue
		}
		kept = append(kept, m)
	}
	s.queue = kept
}

// outboxView は Stores を outbox.MessageStore として見せる読み書きビュー。
// キュー削除（MarkPublished）は恒久ログに影響しない一方、Enqueue は両方へ書く。
type outboxView struct {
	s *Stores
}

// コンパイル時に outbox.MessageStore を満たしていることを確認する。
var _ outbox.MessageStore = outboxView{}

// Enqueue はメッセージを配送キューと恒久ログの両方へ書く（PostgreSQL アダプタの Enqueue が
// InsertOutboxMessage と InsertEvent を同一トランザクションで実行するのと同じ意味論）。
func (v outboxView) Enqueue(_ context.Context, m outbox.Message) error {
	v.s.enqueue(m)
	return nil
}

// Unpublished は未送信のメッセージを最大 limit 件返す。
func (v outboxView) Unpublished(_ context.Context, limit int) ([]outbox.Message, error) {
	return v.s.unpublished(limit), nil
}

// MarkPublished は送信に成功したメッセージを配送キューから取り除く（恒久ログは残る）。
func (v outboxView) MarkPublished(_ context.Context, ids ...string) error {
	v.s.markPublished(ids...)
	return nil
}
