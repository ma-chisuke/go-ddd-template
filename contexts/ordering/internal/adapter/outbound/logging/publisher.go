package logging

import (
	"context"
	"log/slog"

	"github.com/example/go-ddd-template/shared/outbox"
)

// Publisher はアウトボックス送信の「開発用（no-op）」実装。実際のトランスポート
// （在庫サービスへの HTTP push）を持たない代わりに、送出しようとしたメッセージを
// 構造化ログに記録するだけで成功として扱う。
//
// 本番の分散構成では eventhttp.Publisher（在庫の event-ingest への HTTP push）を用いる。
// この no-op はトランスポート未注入時（テストやローカルの単体起動）の安全な既定値である。
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
