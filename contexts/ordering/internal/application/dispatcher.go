package application

import (
	"context"

	"github.com/example/go-ddd-template/contexts/ordering/internal/domain/order"
)

// EventDispatcher はドメインイベントをプロセス内で配信するポート。
// ユースケースは永続化の成功後にのみ、このポートを通じてイベントを配信する。
// エラーを返さないのは、これが後処理であり、コミット済みのトランザクションを
// 巻き戻せないためである（ハンドラのエラーは実装側がログに残す）。
//
// 実装は共有モジュールの型付きディスパッチャ event.Typed[order.DomainEvent] が提供し、
// 合成ルート（ordering.go）で結線する。ポートはこのコンテキストのドメイン型で宣言され、
// 実装は共有機構 — 機構は共有し、型はコンテキスト固有に保つ、という境界の引き方である。
//
// このポートが扱うのは「プロセス内のみ」のイベント（v1 の OrderPlaced）である。
// クロスコンテキストイベント（OrderCancelled）は、これとは別に、ユースケースが同一 UoW 内で
// 翻訳済み契約へ変換してアウトボックスへ積む（[messages.go] 参照）。
type EventDispatcher interface {
	Dispatch(ctx context.Context, events ...order.DomainEvent)
}
