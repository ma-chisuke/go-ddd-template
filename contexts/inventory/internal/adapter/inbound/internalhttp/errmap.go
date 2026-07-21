package internalhttp

import (
	"context"
	"errors"
	"net/http"
	"net/url"

	"github.com/example/go-ddd-template/contexts/inventory/internal/adapter/inbound/openapiinternal"
	"github.com/example/go-ddd-template/contexts/inventory/internal/domain/inventory"
	"github.com/example/go-ddd-template/shared/outbox"
	"github.com/example/go-ddd-template/shared/uow"
)

// problemTypeBlank は RFC 9457 の既定の type 値。
var problemTypeBlank = mustParseURL("about:blank")

// NewError はハンドラが返したエラーを RFC 9457 (Problem Details) 形式の応答へ翻訳する。
//
//   - ErrStockItemNotFound / ErrReservationNotFound      -> 404 Not Found
//   - ErrInsufficientStock / ErrConcurrencyConflict       -> 409 Conflict
//   - ErrInvalidSKU / ErrInvalidQuantity /
//     ErrInvalidReservationRef / ErrNoRoute               -> 422 Unprocessable Entity
//   - それ以外                                             -> 500 Internal Server Error
//
// クライアント起因のエラー（4xx）は detail に理由を載せるが、サーバ起因（5xx）は
// 内部情報を漏らさないよう一般的な文言に留め、詳細はログにのみ残す。
func (h *Handler) NewError(ctx context.Context, err error) *openapiinternal.ProblemResponseStatusCode {
	status, title := classify(err)

	detail := err.Error()
	if status >= http.StatusInternalServerError {
		h.log.ErrorContext(ctx, "内部エラーが発生しました", "error", err)
		detail = "予期しないエラーが発生しました"
	} else {
		h.log.WarnContext(ctx, "リクエストを処理できませんでした", "status", status, "error", err)
	}

	return &openapiinternal.ProblemResponseStatusCode{
		StatusCode: status,
		Response: openapiinternal.ProblemDetails{
			Type:   problemTypeBlank,
			Title:  title,
			Status: status,
			Detail: openapiinternal.NewOptString(detail),
		},
	}
}

// classify はエラーを HTTP ステータスとタイトルに対応づける。
func classify(err error) (int, string) {
	switch {
	case errors.Is(err, inventory.ErrStockItemNotFound), errors.Is(err, inventory.ErrReservationNotFound):
		return http.StatusNotFound, "Not Found"
	case errors.Is(err, inventory.ErrInsufficientStock), errors.Is(err, uow.ErrConcurrencyConflict):
		return http.StatusConflict, "Conflict"
	case errors.Is(err, inventory.ErrInvalidSKU),
		errors.Is(err, inventory.ErrInvalidQuantity),
		errors.Is(err, inventory.ErrInvalidReservationRef),
		errors.Is(err, outbox.ErrNoRoute):
		return http.StatusUnprocessableEntity, "Unprocessable Entity"
	default:
		return http.StatusInternalServerError, "Internal Server Error"
	}
}

func mustParseURL(raw string) url.URL {
	u, err := url.Parse(raw)
	if err != nil {
		panic("internalhttp: URL の解析に失敗しました: " + raw)
	}
	return *u
}
