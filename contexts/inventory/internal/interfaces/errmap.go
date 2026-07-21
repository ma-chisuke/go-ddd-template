package interfaces

import (
	"context"
	"errors"
	"net/http"
	"net/url"

	"github.com/example/go-ddd-template/contexts/inventory/internal/domain/inventory"
	"github.com/example/go-ddd-template/contexts/inventory/internal/interfaces/openapi"
	"github.com/example/go-ddd-template/shared/uow"
)

// problemTypeBlank は RFC 9457 の既定の type 値。
var problemTypeBlank = mustParseURL("about:blank")

// NewError はハンドラが返したエラーを RFC 9457 (Problem Details) 形式の応答へ翻訳する。
// ogen は default 応答の宣言からこのメソッドの実装を要求する。
//
//   - ErrStockItemNotFound         -> 404 Not Found
//   - ErrInvalidSKU / ErrInvalidQuantity -> 422 Unprocessable Entity（入力検証）
//   - ErrConcurrencyConflict       -> 409 Conflict（楽観的排他制御の衝突）
//   - それ以外                       -> 500 Internal Server Error
//
// クライアント起因のエラー（4xx）は detail に理由を載せるが、サーバ起因（5xx）は
// 内部情報を漏らさないよう一般的な文言に留め、詳細はログにのみ残す。
func (h *Handler) NewError(ctx context.Context, err error) *openapi.ProblemResponseStatusCode {
	status, title := classify(err)

	detail := err.Error()
	if status >= http.StatusInternalServerError {
		h.log.ErrorContext(ctx, "内部エラーが発生しました", "error", err)
		detail = "予期しないエラーが発生しました"
	} else {
		h.log.WarnContext(ctx, "リクエストを処理できませんでした",
			"status", status, "error", err)
	}

	return &openapi.ProblemResponseStatusCode{
		StatusCode: status,
		Response: openapi.ProblemDetails{
			Type:   problemTypeBlank,
			Title:  title,
			Status: status,
			Detail: openapi.NewOptString(detail),
		},
	}
}

// classify はエラーを HTTP ステータスとタイトルに対応づける。
func classify(err error) (int, string) {
	switch {
	case errors.Is(err, inventory.ErrStockItemNotFound):
		return http.StatusNotFound, "Not Found"
	case errors.Is(err, inventory.ErrInvalidSKU), errors.Is(err, inventory.ErrInvalidQuantity):
		return http.StatusUnprocessableEntity, "Unprocessable Entity"
	case errors.Is(err, uow.ErrConcurrencyConflict):
		return http.StatusConflict, "Conflict"
	default:
		return http.StatusInternalServerError, "Internal Server Error"
	}
}

func mustParseURL(raw string) url.URL {
	u, err := url.Parse(raw)
	if err != nil {
		panic("interfaces: URL の解析に失敗しました: " + raw)
	}
	return *u
}
