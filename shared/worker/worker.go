// Package worker は背景ワーカーを安全に走らせるための小さなヘルパーを提供する。
//
// このパッケージはドメインにもコンテキスト固有コードにも依存しない技術的な建材で、
// 各コンテキストのファサードが背景ループ（アウトボックス送信中継や期限切れ掃除など）を
// 起動する際に共有する。
package worker

import (
	"context"
	"log/slog"
)

// Safely は fn を recover-and-log で包んで実行する（panic でサービスを巻き込まない）。
// fn の中で panic が起きても、name を添えてログに記録したうえで回復し、呼び出し元へ
// 伝播させない。背景ワーカーの 1 ループが想定外の panic でサービス全体を落とさないための
// 隔離境界である。
//
// ticker ループそのものは呼び出し側が持つ（このパッケージはループを提供しない）。
// 呼び出し元が 1 つしかないループ機構を先回りして共通化しないためである。
func Safely(ctx context.Context, log *slog.Logger, name string, fn func(context.Context)) {
	defer func() {
		if r := recover(); r != nil {
			log.ErrorContext(ctx, "背景ワーカーが panic から回復しました", "worker", name, "panic", r)
		}
	}()
	fn(ctx)
}
