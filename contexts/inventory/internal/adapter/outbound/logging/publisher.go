package logging

import (
	"context"
	"log/slog"

	"github.com/example/go-ddd-template/shared/outbox"
)

// Publisher はアウトボックス送信の「開発用（no-op）」実装。実際のトランスポート
// （メッセージブローカーや HTTP push）を持たない代わりに、送出しようとしたメッセージを
// 構造化ログに記録するだけで成功として扱う。
//
// このスライスの在庫コンテキストはクロスコンテキストへの送信を行わない（アウトボックスは
// 常に空）ため、この Publisher が実際に呼ばれることはないが、送信中継（outbox.Runner）を
// 結線するために Publisher 実装を用意しておく。本番トランスポートが必要になれば差し替える。
type Publisher struct {
	log *slog.Logger
}

// NewPublisher は開発用（no-op）のパブリッシャを生成する。
func NewPublisher(log *slog.Logger) *Publisher {
	return &Publisher{log: log}
}

// コンパイル時に outbox.Publisher を満たしていることを確認する。
var _ outbox.Publisher = (*Publisher)(nil)

// Publish は送出の事実をログに記録するだけで成功する（実トランスポートなし）。
func (p *Publisher) Publish(ctx context.Context, m outbox.Message) error {
	p.log.InfoContext(ctx, "アウトボックスメッセージを送出しました（開発用 no-op）",
		slog.String("id", m.ID),
		slog.String("type", m.Type),
		slog.String("trace_id", m.TraceID),
	)
	return nil
}
