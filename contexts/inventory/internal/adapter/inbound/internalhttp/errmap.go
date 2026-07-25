package internalhttp

import (
	"context"
	"errors"
	"net/http"
	"net/url"

	"github.com/example/go-ddd-template/contexts/inventory/internal/adapter/inbound/openapiinternal"
	"github.com/example/go-ddd-template/contexts/inventory/internal/domain/inventory"
	"github.com/example/go-ddd-template/shared/outbox"
	"github.com/example/go-ddd-template/shared/problem"
	"github.com/example/go-ddd-template/shared/uow"
)

// NewError はハンドラが返したエラー（E4）を RFC 9457 (Problem Details) 形式の応答へ
// 翻訳する。
//
//   - ErrStockItemNotFound / ErrReservationNotFound      -> 404 Not Found
//   - ErrInsufficientStock / ErrConcurrencyConflict       -> 409 Conflict
//   - ErrInvalidSKU / ErrInvalidQuantity /
//     ErrInvalidReservationRef / ErrNoRoute               -> 422 Unprocessable Entity
//   - それ以外                                             -> 500 Internal Server Error
//
// **detail に err.Error() を載せない**（規則 R-11 / FR-2.4）。とくにこの内部 API は
// ErrStockItemNotFound / ErrInsufficientStock を返す経路を持ち、それらのエラー文言は
// SKU・要求数量・利用可能在庫を含む。定型文へ置き換えて外へ出さず、ログにだけ残す
// （FR-2.5 / 規則 R-13）。
//
// 呼び出し側の腐敗防止層（ordering の aclhttp）はステータスコードだけを見るため、
// この変更でも翻訳結果は変わらない（規則 R-16）。
func (h *Handler) NewError(ctx context.Context, err error) *openapiinternal.ProblemResponseStatusCode {
	status, statusText := classify(err)
	suffix := problemTypeSuffix(status)

	h.logProblem(ctx, status, err)

	return &openapiinternal.ProblemResponseStatusCode{
		StatusCode: status,
		Response: openapiinternal.ProblemDetails{
			Type:   problemTypeOf(suffix),
			Title:  problem.TitleOf(suffix, statusText),
			Status: status,
			Detail: openapiinternal.NewOptString(detailOf(suffix)),
			// 422 のときだけ、アプリケーション層が解決したフィールドが載る。
			// 他の経路（404 / 409 / 5xx）は入力フィールドに帰着しないので nil になり、
			// invalid-params はキーごと省略される（規則 R-14）。
			InvalidParams: domainParams(err),
		},
	}
}

// problemTypeSuffix はステータスを type URI の種別サフィックスへ対応づける。
//
// classify（ステータス判定）と別の関数にしているのは、既存の classify を無変更で
// 保つためである（規則 R-15 の後方互換条件）。
func problemTypeSuffix(status int) string {
	switch status {
	case http.StatusNotFound:
		// E2（そのような経路が無い）とは別の種別。ここは「経路はあるが対象が無い」。
		return problem.TypeResourceNotFound
	case http.StatusConflict:
		return problem.TypeConflict
	case http.StatusUnprocessableEntity:
		return problem.TypeInvalidInput
	default:
		return problem.TypeInternalError
	}
}

// detailOf は種別サフィックスに対応する detail の定型文を返す（規則 R-12）。
func detailOf(suffix string) string {
	switch suffix {
	case problem.TypeResourceNotFound:
		return problem.DetailResourceNotFound
	case problem.TypeConflict:
		return problem.DetailConflict
	case problem.TypeInvalidInput:
		return problem.DetailInvalidInput
	default:
		// 5xx は内部情報を漏らさないよう一般的な文言に留める（詳細はログにのみ残す）。
		return problem.DetailInternalError
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
