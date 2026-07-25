package application

import (
	"context"

	"github.com/example/go-ddd-template/contexts/inventory/internal/domain/inventory"
)

// EventDispatcher はドメインイベントをプロセス内で配信するポート。
// ユースケースは永続化の成功後にのみ、このポートを通じてイベントを配信する。
// エラーを返さないのは、これが後処理であり、コミット済みのトランザクションを
// 巻き戻せないためである（ハンドラのエラーは実装側がログに残す）。
//
// 実装は共有モジュールの型付きディスパッチャ event.Typed[inventory.DomainEvent] が提供し、
// 合成ルート（inventory.go）で結線する。ポートはこのコンテキストのドメイン型で宣言され、
// 実装は共有機構 — 機構は共有し、型はコンテキスト固有に保つ、という境界の引き方である。
//
// このスライスでは購読側はログ出力／記録が中心で、外部への非同期配信は行わない。
// より強い配信保証が必要になれば、アウトボックス方式（shared/outbox）へ差し替えられるよう、
// このポートを挟んでいる。
type EventDispatcher interface {
	Dispatch(ctx context.Context, events ...inventory.DomainEvent)
}
