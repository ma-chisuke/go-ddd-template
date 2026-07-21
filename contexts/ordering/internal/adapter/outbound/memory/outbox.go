package memory

import (
	"context"
	"sync"

	"github.com/example/go-ddd-template/shared/outbox"
)

// OutboxStore はインメモリのアウトボックス（送信側ストア）。確定済みメッセージと
// 送信済みフラグを保持する。UnitOfWork のコミット時に確定メッセージが積まれ、
// 送信中継（outbox.Runner）が Unpublished / MarkPublished で読み書きする。
//
// outbox.MessageStore を満たすため Enqueue も持つが、集約書き込みと同一トランザクションで
// 積むには UnitOfWork 経由（repos.Outbox().Enqueue）を使うこと。直接の Enqueue は
// 即時コミット扱いになる。
type OutboxStore struct {
	mu        sync.Mutex
	msgs      []outbox.Message
	published map[string]bool
}

// NewOutboxStore は空のアウトボックスストアを生成する。
func NewOutboxStore() *OutboxStore {
	return &OutboxStore{published: make(map[string]bool)}
}

// コンパイル時に outbox.MessageStore を満たしていることを確認する。
var _ outbox.MessageStore = (*OutboxStore)(nil)

// Enqueue はメッセージを即時に確定させて積む（UoW を経由しない直接利用）。
func (s *OutboxStore) Enqueue(_ context.Context, m outbox.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.msgs = append(s.msgs, m)
	return nil
}

// Unpublished は未送信のメッセージを最大 limit 件返す。
func (s *OutboxStore) Unpublished(_ context.Context, limit int) ([]outbox.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []outbox.Message
	for _, m := range s.msgs {
		if s.published[m.ID] {
			continue
		}
		out = append(out, m)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

// MarkPublished は指定 ID のメッセージを送信済みとして記録する。
func (s *OutboxStore) MarkPublished(_ context.Context, ids ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range ids {
		s.published[id] = true
	}
	return nil
}

// Messages は積まれた全メッセージのコピーを返す（テストでの検証用）。
func (s *OutboxStore) Messages() []outbox.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]outbox.Message, len(s.msgs))
	copy(out, s.msgs)
	return out
}

// appendCommitted は UnitOfWork のコミット時に、ステージされたメッセージを確定させる。
func (s *OutboxStore) appendCommitted(msgs []outbox.Message) {
	if len(msgs) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.msgs = append(s.msgs, msgs...)
}
