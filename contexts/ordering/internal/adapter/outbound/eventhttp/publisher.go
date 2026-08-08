// Package eventhttp は、アウトボックスの送信トランスポート（outbox.Publisher）を
// 在庫コンテキストの内部 API のメッセージ取り込みエンドポイント（POST /events）への
// HTTP push で実装する送信アダプタ。
//
// 注文コンテキストのアウトボックス中継（outbox.Runner）が未送信メッセージをポーリングし、
// この Publisher が在庫サービスへ届ける。これにより ConfirmReservation コマンドと
// OrderCancelled イベントが実際にサービスを跨いで在庫側の購読ポリシへ到達する。
// 送信には生成クライアント clients/inventory を用い、contexts/inventory は import しない。
package eventhttp

import (
	"context"
	"fmt"

	"github.com/example/go-ddd-template/clients/inventory/invclient"
	"github.com/example/go-ddd-template/shared/outbox"
)

// Publisher は outbox.Publisher を在庫の event-ingest エンドポイントへの HTTP push で実装する。
type Publisher struct {
	client invclient.Invoker
}

// NewPublisher は送信トランスポートを生成する。
func NewPublisher(client invclient.Invoker) *Publisher {
	return &Publisher{client: client}
}

// コンパイル時に outbox.Publisher を満たしていることを確認する。
var _ outbox.Publisher = (*Publisher)(nil)

// Publish は 1 件のメッセージを在庫の event-ingest エンドポイントへ送出する。
// payload は翻訳済み契約のシリアライズ（JSON 文字列）としてそのまま運ぶ。TraceID を
// 添えることで、送出先の在庫サービスまで相関 ID が伝播する。
//
// 失敗はエラーとして返す（送信中継が at-least-once で次周期に再送する）。受信側 Consumer は
// 冪等なので再送は安全。
func (p *Publisher) Publish(ctx context.Context, m outbox.Message) error {
	msg := &invclient.InboundMessage{
		ID:      m.ID,
		Type:    m.Type,
		Payload: string(m.Payload),
	}
	if m.TraceID != "" {
		msg.TraceID = invclient.NewOptString(m.TraceID)
	}
	if !m.OccurredAt.IsZero() {
		msg.OccurredAt = invclient.NewOptDateTime(m.OccurredAt)
	}
	if _, err := p.client.IngestEvent(ctx, msg); err != nil {
		return fmt.Errorf("在庫サービスへのメッセージ送出に失敗しました（id=%s type=%s）: %w", m.ID, m.Type, err)
	}
	return nil
}
