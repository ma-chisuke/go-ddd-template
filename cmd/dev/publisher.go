package main

import (
	"context"

	inventory "github.com/example/go-ddd-template/contexts/inventory"
	"github.com/example/go-ddd-template/shared/outbox"
)

// syncDeliverPublisher は注文コンテキストのアウトボックス送信トランスポート（outbox.Publisher）を、
// 在庫モジュールの受信シーム（inventory.Module.Deliver）を直接呼んで満たす同一プロセス向けの
// 同期パブリッシャ。分散構成の eventhttp（在庫の /events への HTTP push）と同じポート契約を満たす。
//
// このパブリッシャは、注文の作業単位がコミットした直後に（開発用の同期配送シンクから）呼ばれ、
// メッセージをその場でピア（在庫）の outbox.Router へ届ける。store も poll も介さない、
// 決定的な配送である。
//
// 重要（実証範囲の明示）: この同期配送は、注文が在庫のドメイン型を知らずに「翻訳済み契約
// （message_type + payload）」だけでピアへ到達する **decoupling** を示す。しかし、実運用の
// **遅延ある eventual consistency（結果整合）のタイミング** は示さない。遅延を伴う本物の
// 結果整合は、PostgreSQL のアウトボックス + 送信中継（docker-compose 経路）で観察できる。
type syncDeliverPublisher struct {
	inv *inventory.Module
}

// コンパイル時に outbox.Publisher を満たしていることを確認する。
var _ outbox.Publisher = (*syncDeliverPublisher)(nil)

// Publish は 1 件のメッセージをピア（在庫）の受信経路へ同期的に届ける。種別（message_type）に
// 応じて在庫側の購読ポリシ（OnConfirmReservation / OnOrderCancelled）へ振り分けられる。
func (p *syncDeliverPublisher) Publish(ctx context.Context, m outbox.Message) error {
	return p.inv.Deliver(ctx, m)
}

// noopPublisher は何も送出しない outbox.Publisher。在庫コンテキストは v1 では
// クロスコンテキストへの送信を行わないため、在庫モジュールの送信トランスポートに用いる。
type noopPublisher struct{}

// コンパイル時に outbox.Publisher を満たしていることを確認する。
var _ outbox.Publisher = noopPublisher{}

// Publish は何もしない（在庫はクロスコンテキストメッセージを発行しない）。
func (noopPublisher) Publish(_ context.Context, _ outbox.Message) error { return nil }
