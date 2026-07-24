package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/example/go-ddd-template/contexts/inventory/internal/adapter/outbound/postgres/sqlcgen"
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

// Enqueue はメッセージを outbox テーブル（一時的な配送キュー）と events テーブル
// （恒久イベントログ）の両方へ挿入する。両方の書き込みは同じ tx 束縛の Queries（s.q）で
// 行うため、UoW 経由で呼べば集約書き込みと合わせて 1 つのトランザクションに参加する。
// これにより「outbox に積んだが events に無い」状態は構造的に起きない。
// TraceID が空なら context の相関 ID を刻む（両表に同じ値を入れる）。
func (s *outboxStore) Enqueue(ctx context.Context, m outbox.Message) error {
	traceID := m.TraceID
	if traceID == "" {
		traceID = correlation.FromContextOrEmpty(ctx)
	}
	occurredAt := pgtype.Timestamptz{Time: m.OccurredAt, Valid: true}

	// 配送キューへ積む（送信成功後に削除される一時的な行）。
	err := s.q.InsertOutboxMessage(ctx, sqlcgen.InsertOutboxMessageParams{
		ID:          m.ID,
		MessageType: m.Type,
		Payload:     m.Payload,
		TraceID:     traceID,
		OccurredAt:  occurredAt,
	})
	if err != nil {
		return fmt.Errorf("アウトボックスへの書き込みに失敗しました: %w", err)
	}

	// 恒久イベントログへ記録する（同一トランザクション・追記のみ）。
	err = s.q.InsertEvent(ctx, sqlcgen.InsertEventParams{
		ID:          m.ID,
		MessageType: m.Type,
		Payload:     m.Payload,
		TraceID:     traceID,
		OccurredAt:  occurredAt,
	})
	if err != nil {
		return fmt.Errorf("イベントログへの書き込みに失敗しました: %w", err)
	}
	return nil
}

// Unpublished は未送信メッセージを最大 limit 件返す。
// 送信済みの行は削除されているため、outbox に残っている行はすべて未送信である。
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

// MarkPublished は送信に成功したメッセージを配送キューから削除する
// （delete-after-publish）。発行履歴は events テーブルに残るため記録は失われない。
func (s *outboxStore) MarkPublished(ctx context.Context, ids ...string) error {
	for _, id := range ids {
		if err := s.q.MarkOutboxPublished(ctx, id); err != nil {
			return fmt.Errorf("メッセージ %q の配送キューからの削除に失敗しました: %w", id, err)
		}
	}
	return nil
}

// NewOutboxStore はコネクションプールに直結した outbox.MessageStore を返す。
// 送信中継（outbox.Runner）が Unpublished / MarkPublished に用いる。
func NewOutboxStore(pool *pgxpool.Pool) outbox.MessageStore {
	return newOutboxStore(sqlcgen.New(pool))
}
