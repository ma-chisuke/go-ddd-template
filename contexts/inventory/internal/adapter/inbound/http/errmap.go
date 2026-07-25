package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/url"

	"github.com/example/go-ddd-template/contexts/inventory/internal/adapter/inbound/openapi"
	"github.com/example/go-ddd-template/contexts/inventory/internal/domain/inventory"
	"github.com/example/go-ddd-template/shared/problem"
	"github.com/example/go-ddd-template/shared/uow"
)

// NewError はハンドラが返したエラー（E4）を RFC 9457 (Problem Details) 形式の応答へ
// 翻訳する。ogen は default 応答の宣言からこのメソッドの実装を要求する。
//
//   - ErrStockItemNotFound         -> 404 Not Found
//   - ErrInvalidSKU / ErrInvalidQuantity -> 422 Unprocessable Entity（入力検証）
//   - ErrConcurrencyConflict       -> 409 Conflict（楽観的排他制御の衝突）
//   - それ以外                       -> 500 Internal Server Error
//
// **detail に err.Error() を載せない**（規則 R-11 / FR-2.4）。ドメインのエラー文言は
// SKU・要求数量・利用可能在庫といった受信値や内部状態を含むため、経路ごとの定型文へ
// 置き換える。排除した情報はログにだけ残す（FR-2.5 / 規則 R-13）。
func (h *Handler) NewError(ctx context.Context, err error) *openapi.ProblemResponseStatusCode {
	status, statusText := classify(err)
	suffix := problemTypeSuffix(status)

	h.logProblem(ctx, status, err)

	return &openapi.ProblemResponseStatusCode{
		StatusCode: status,
		Response: openapi.ProblemDetails{
			Type:   problemTypeOf(suffix),
			Title:  problem.TitleOf(suffix, statusText),
			Status: status,
			Detail: openapi.NewOptString(detailOf(suffix)),
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
// 保つためである（規則 R-15 の後方互換条件）。公開 API はステータスごとに原因が
// 1 つなので、注文コンテキストと違いエラーの中身を見る必要はない。
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
