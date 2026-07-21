package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/example/go-ddd-template/contexts/ordering/internal/adapter/outbound/postgres/sqlcgen"
	"github.com/example/go-ddd-template/shared/correlation"
	"github.com/example/go-ddd-template/shared/outbox"
)

// outboxStore は sqlc が生成した Queries を用いた outbox.MessageStore アダプタ。
// 書き込み用（トランザクション束縛）と送信中継用（プール直結）の両方でこの型を使える。
type outboxStore struct {
	q *sqlcgen.Queries
}

func newOutboxStore(q *sqlcgen.Queries) *outboxStore {
	return &outboxStore{q: q}
}

// コンパイル時に outbox.MessageStore を満たしていることを確認する。
var _ outbox.MessageStore = (*outboxStore)(nil)

// Enqueue はメッセージを outbox テーブルへ挿入する。UoW 経由で呼べば集約書き込みと
// 同一トランザクションに参加する。TraceID が空なら context の相関 ID を刻む。
func (s *outboxStore) Enqueue(ctx context.Context, m outbox.Message) error {
	traceID := m.TraceID
	if traceID == "" {
		traceID = correlation.FromContextOrEmpty(ctx)
	}
	err := s.q.InsertOutboxMessage(ctx, sqlcgen.InsertOutboxMessageParams{
		ID:          m.ID,
		MessageType: m.Type,
		Payload:     m.Payload,
		TraceID:     traceID,
		OccurredAt:  pgtype.Timestamptz{Time: m.OccurredAt, Valid: true},
	})
	if err != nil {
		return fmt.Errorf("アウトボックスへの書き込みに失敗しました: %w", err)
	}
	return nil
}

// Unpublished は未送信メッセージを最大 limit 件返す。
func (s *outboxStore) Unpublished(ctx context.Context, limit int) ([]outbox.Message, error) {
	rows, err := s.q.ListUnpublishedOutbox(ctx, int32(limit))
	if err != nil {
		return nil, fmt.Errorf("未送信メッセージの取得に失敗しました: %w", err)
	}
	msgs := make([]outbox.Message, 0, len(rows))
	for _, r := range rows {
		msgs = append(msgs, outbox.Message{
			ID:         r.ID,
			Type:       r.MessageType,
			Payload:    r.Payload,
			TraceID:    r.TraceID,
			OccurredAt: r.OccurredAt.Time,
		})
	}
	return msgs, nil
}

// MarkPublished は指定 ID のメッセージを送信済みとして記録する。
func (s *outboxStore) MarkPublished(ctx context.Context, ids ...string) error {
	for _, id := range ids {
		if err := s.q.MarkOutboxPublished(ctx, id); err != nil {
			return fmt.Errorf("メッセージ %q の送信済み記録に失敗しました: %w", id, err)
		}
	}
	return nil
}

// NewOutboxStore はコネクションプールに直結した outbox.MessageStore を返す。
// 送信中継（outbox.Runner）が Unpublished / MarkPublished に用いる。
func NewOutboxStore(pool *pgxpool.Pool) outbox.MessageStore {
	return newOutboxStore(sqlcgen.New(pool))
}
