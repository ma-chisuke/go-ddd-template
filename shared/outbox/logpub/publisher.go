// Package logpub は outbox.Publisher の開発用（no-op）実装を提供する。
// 実トランスポートを持たない構成での安全な既定値であり、送出したいメッセージを
// 構造化ログに記録するだけで成功として扱う。ドメインにもコンテキスト固有コードにも
// 依存しないため、どの境界づけられたコンテキストからでも共有できる。
package logpub

import (
	"context"
	"log/slog"

	"github.com/example/go-ddd-template/shared/outbox"
)

// Publisher はアウトボックス送信の「開発用（no-op）」実装。実際のトランスポート
// （メッセージブローカーやピアサービスへの HTTP push）を持たない代わりに、送出しようと
// したメッセージを構造化ログに記録するだけで成功として扱う。
//
// これはトランスポート未注入時（テストやローカルの単体起動、あるいはそのスライスでは
// クロスコンテキスト送信を行わない構成）の安全な既定値である。実トランスポートが必要に
// なれば、配置側が本番用の Publisher へ差し替える。
type Publisher struct {
	log *slog.Logger
}

// New は開発用（no-op）のパブリッシャを生成する。
func New(log *slog.Logger) *Publisher {
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
