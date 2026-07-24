package memory

import (
	"sync"

	"github.com/example/go-ddd-template/shared/outbox"
)

// EventStore はインメモリの恒久イベントログ（追記専用）。PostgreSQL の inventory.events
// テーブルに対応するインメモリ実装で、発行したメッセージの記録を保持する。
//
// アウトボックス（OutboxStore）が「一時的な配送キュー」で送信後に行を削除するのに対し、
// こちらは何も削除しない追記専用のログである。UnitOfWork のコミット時に、その
// トランザクションでステージされたメッセージがアウトボックスと同時に追記されるため、
// 「アウトボックスに積んだがイベントログに無い」状態は起きない。
//
// 配送は駆動しない（outbox.Runner はこの型を参照しない）。読み出しはテストや
// 開発ハーネス（cmd/dev）からの検証用に Messages() だけを公開する。
type EventStore struct {
	mu   sync.Mutex
	msgs []outbox.Message
}

// NewEventStore は空のイベントログを生成する。
func NewEventStore() *EventStore {
	return &EventStore{}
}

// Messages は記録済みの全メッセージのコピーを発生順に返す（検証用）。
// 呼び出し側が返り値を書き換えても内部状態は壊れない。
func (s *EventStore) Messages() []outbox.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]outbox.Message, len(s.msgs))
	copy(out, s.msgs)
	return out
}

// Len は記録済みのメッセージ件数を返す（検証用）。
func (s *EventStore) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.msgs)
}

// appendCommitted は UnitOfWork のコミット時に、ステージされたメッセージを追記する。
// イベントログは追記専用のため、ここで積まれた記録が削除されることはない。
func (s *EventStore) appendCommitted(msgs []outbox.Message) {
	if len(msgs) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.msgs = append(s.msgs, msgs...)
}
