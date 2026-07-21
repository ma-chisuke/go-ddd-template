package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/url"

	"github.com/example/go-ddd-template/contexts/ordering/internal/adapter/inbound/openapi"
	"github.com/example/go-ddd-template/contexts/ordering/internal/application"
	"github.com/example/go-ddd-template/contexts/ordering/internal/domain/order"
	"github.com/example/go-ddd-template/shared/uow"
)

// problemTypeBlank は RFC 9457 の既定の type 値。
var problemTypeBlank = mustParseURL("about:blank")

// NewError はハンドラが返したエラーを RFC 9457 (Problem Details) 形式の応答へ翻訳する。
// ogen は default 応答の宣言からこのメソッドの実装を要求する。
//
//   - ErrReservationUnavailable         -> 503 Service Unavailable（在庫サービス不達）
//   - ErrReservationRejected            -> 409 Conflict（在庫予約の拒否）
//   - ErrOrderNotFound                  -> 404 Not Found
//   - ErrOrderNotConfirmed / 版衝突      -> 409 Conflict（現在状態と矛盾）
//   - 入力検証（ErrEmptyOrder / ErrInvalid*）-> 422 Unprocessable Entity
//   - それ以外                            -> 500 Internal Server Error
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
		h.log.WarnContext(ctx, "リクエストを処理できませんでした", "status", status, "error", err)
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
//
// ErrReservationUnavailable を ErrReservationRejected より先に判定するのが要点。
// 不達系の失敗は両方の番兵に一致（errors.Join）するため、先に不達（503）を確定させる。
func classify(err error) (int, string) {
	switch {
	case errors.Is(err, application.ErrReservationUnavailable):
		return http.StatusServiceUnavailable, "Service Unavailable"
	case errors.Is(err, application.ErrReservationRejected):
		return http.StatusConflict, "Conflict"
	case errors.Is(err, order.ErrOrderNotFound):
		return http.StatusNotFound, "Not Found"
	case errors.Is(err, order.ErrOrderNotConfirmed), errors.Is(err, uow.ErrConcurrencyConflict):
		return http.StatusConflict, "Conflict"
	case errors.Is(err, order.ErrEmptyOrder),
		errors.Is(err, order.ErrInvalidSKU),
		errors.Is(err, order.ErrInvalidQuantity),
		errors.Is(err, order.ErrInvalidMoney),
		errors.Is(err, order.ErrInvalidCustomerID),
		errors.Is(err, order.ErrInvalidOrderID),
		errors.Is(err, order.ErrInvalidReservationRef):
		return http.StatusUnprocessableEntity, "Unprocessable Entity"
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
