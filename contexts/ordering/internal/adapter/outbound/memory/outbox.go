package memory

import (
	"context"
	"sync"

	"github.com/example/go-ddd-template/shared/outbox"
)

// OutboxStore はインメモリのアウトボックス（送信側ストア）。これは「一時的な配送キュー」で
// あり、未送信メッセージだけを保持する。UnitOfWork のコミット時に確定メッセージが積まれ、
// 送信中継（outbox.Runner）が Unpublished / MarkPublished で読み書きする。
// 送信に成功したメッセージは MarkPublished で取り除かれるため（delete-after-publish）、
// この型に残っているものは常に未送信である。発行の恒久的な記録は EventStore が担う。
//
// outbox.MessageStore を満たすため Enqueue も持つが、集約書き込みと同一トランザクションで
// 積むには UnitOfWork 経由（repos.Outbox().Enqueue）を使うこと。直接の Enqueue は
// 即時コミット扱いになる（イベントログには記録されない）。
type OutboxStore struct {
	mu   sync.Mutex
	msgs []outbox.Message
}

// NewOutboxStore は空のアウトボックスストアを生成する。
func NewOutboxStore() *OutboxStore {
	return &OutboxStore{}
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
// 送信済みは削除済みのため、保持している全メッセージが未送信である。
func (s *OutboxStore) Unpublished(_ context.Context, limit int) ([]outbox.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []outbox.Message
	for _, m := range s.msgs {
		out = append(out, m)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

// MarkPublished は送信に成功したメッセージを配送キューから取り除く
// （delete-after-publish）。発行の記録は EventStore に残るため失われない。
func (s *OutboxStore) MarkPublished(_ context.Context, ids ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	drop := make(map[string]bool, len(ids))
	for _, id := range ids {
		drop[id] = true
	}
	kept := make([]outbox.Message, 0, len(s.msgs))
	for _, m := range s.msgs {
		if drop[m.ID] {
			continue
		}
		kept = append(kept, m)
	}
	s.msgs = kept
	return nil
}

// Messages は配送キューに残っている（＝未送信の）全メッセージのコピーを返す（検証用）。
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
