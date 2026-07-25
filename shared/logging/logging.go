// Package logging は log/slog を用いた構造化ログの初期化を提供する。
// context から相関 ID を取り出し、各ログ行へ自動的に付与する。
//
// このパッケージはドメインにもコンテキスト固有コードにも依存せず、相関 ID の
// 取り出しに shared/correlation だけを用いる。どの境界づけられたコンテキストからでも
// 同じ相関 ID 付きロガーを共有できる技術的な建材である。
package logging

import (
	"context"
	"io"
	"log/slog"

	"github.com/example/go-ddd-template/shared/correlation"
)

// New は JSON ハンドラを用いた構造化ロガーを生成する。
// context に相関 ID があれば、すべてのログ行に correlation_id 属性として刻む。
func New(w io.Writer, level slog.Level) *slog.Logger {
	base := slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level})
	return slog.New(&correlationHandler{Handler: base})
}

// correlationHandler は下位ハンドラをラップし、Handle 時に context の相関 ID を
// 属性として追加する slog.Handler。
type correlationHandler struct {
	slog.Handler
}

// Handle は相関 ID を付与してから下位ハンドラへ委譲する。
func (h *correlationHandler) Handle(ctx context.Context, r slog.Record) error {
	if id := correlation.FromContextOrEmpty(ctx); id != "" {
		r.AddAttrs(slog.String("correlation_id", id))
	}
	return h.Handler.Handle(ctx, r)
}

// WithAttrs は相関 ID 付与の振る舞いを保ったまま属性を追加する。
func (h *correlationHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &correlationHandler{Handler: h.Handler.WithAttrs(attrs)}
}

// WithGroup は相関 ID 付与の振る舞いを保ったままグループを追加する。
func (h *correlationHandler) WithGroup(name string) slog.Handler {
	return &correlationHandler{Handler: h.Handler.WithGroup(name)}
}
