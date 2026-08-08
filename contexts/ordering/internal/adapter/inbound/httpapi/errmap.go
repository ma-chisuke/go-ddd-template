package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/url"

	"github.com/example/go-ddd-template/contexts/ordering/internal/adapter/inbound/openapi"
	"github.com/example/go-ddd-template/contexts/ordering/internal/application"
	"github.com/example/go-ddd-template/contexts/ordering/internal/domain"
	"github.com/example/go-ddd-template/shared/problem"
	"github.com/example/go-ddd-template/shared/uow"
)

// NewError はハンドラが返したエラー（E4）を RFC 9457 (Problem Details) 形式の応答へ
// 翻訳する。ogen は default 応答の宣言からこのメソッドの実装を要求する。
//
//   - ErrReservationUnavailable         -> 503 Service Unavailable（在庫サービス不達）
//   - ErrReservationRejected            -> 409 Conflict（在庫予約の拒否）
//   - ErrOrderNotFound                  -> 404 Not Found
//   - ErrOrderNotConfirmed / 版衝突      -> 409 Conflict（現在状態と矛盾）
//   - 入力検証（ErrEmptyOrder / ErrInvalid*）-> 422 Unprocessable Entity
//   - それ以外                            -> 500 Internal Server Error
//
// **detail に err.Error() を載せない**（規則 R-11 / FR-2.4）。ドメインのエラー文言は
// SKU・要求数量・利用可能在庫といった受信値や内部状態を含むため、経路ごとの定型文へ
// 置き換える。排除した情報はログにだけ残す（FR-2.5 / 規則 R-13）。
func (h *Handler) NewError(ctx context.Context, err error) *openapi.ProblemResponseStatusCode {
	status, statusText := classify(err)
	suffix := problemTypeSuffix(err, status)

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

// problemTypeSuffix はエラーを type URI の種別サフィックスへ対応づける。
//
// classify（ステータス判定）と別の関数にしているのは、既存の classify を無変更で
// 保つためである（規則 R-15 の後方互換条件。errors.Is の判定順序に既存の振る舞いが
// 依存している）。また種別は status より細かい: 409 は「現在状態との矛盾」と
// 「在庫予約の拒否」に分かれ、クライアントはその区別で取るべき行動を変えられる（規則 R-2）。
func problemTypeSuffix(err error, status int) string {
	switch status {
	case http.StatusNotFound:
		// E2（そのような経路が無い）とは別の種別。ここは「経路はあるが対象が無い」。
		return problem.TypeResourceNotFound
	case http.StatusConflict:
		// より特殊な種別を先に判定する。同じ 409 でも「在庫予約の拒否」「注文が確定状態でない」
		// 「出荷／注文自身の状態との矛盾」は原因が違い、クライアントの取るべき行動も違う。
		if errors.Is(err, application.ErrReservationRejected) {
			return problem.TypeReservationRejected
		}
		if errors.Is(err, application.ErrOrderNotConfirmedForShipment) {
			return problem.TypeOrderNotConfirmedForShipment
		}
		return problem.TypeConflict
	case http.StatusUnprocessableEntity:
		return problem.TypeInvalidInput
	case http.StatusServiceUnavailable:
		return problem.TypeServiceUnavailable
	default:
		return problem.TypeInternalError
	}
}

// detailOf は種別サフィックスに対応する detail の定型文を返す（規則 R-12）。
func detailOf(suffix string) string {
	switch suffix {
	case problem.TypeResourceNotFound:
		return problem.DetailResourceNotFound
	case problem.TypeConflict, problem.TypeReservationRejected:
		return problem.DetailConflict
	case problem.TypeOrderNotConfirmedForShipment:
		return problem.DetailOrderNotConfirmedForShipment
	case problem.TypeInvalidInput:
		return problem.DetailInvalidInput
	default:
		// 5xx は内部情報を漏らさないよう一般的な文言に留める（詳細はログにのみ残す）。
		return problem.DetailInternalError
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
	case errors.Is(err, domain.ErrOrderNotFound), errors.Is(err, domain.ErrShipmentNotFound):
		return http.StatusNotFound, "Not Found"
	case errors.Is(err, domain.ErrOrderNotConfirmed),
		errors.Is(err, domain.ErrShipmentNotPreparing),
		errors.Is(err, application.ErrOrderNotConfirmedForShipment),
		errors.Is(err, uow.ErrConcurrencyConflict):
		return http.StatusConflict, "Conflict"
	// 番兵を errors.Is で明示列挙する。型（domain.FieldViolation）で書いてはならない —
	// 型で判定すると一覧の抜けに気づけず、たとえば空の追跡番号が契約の宣言する 422 ではなく
	// 500 へ落ちる。番兵を 1 つ足したらこの列挙にも 1 行足す。
	case errors.Is(err, domain.ErrEmptyOrder),
		errors.Is(err, domain.ErrInvalidSKU),
		errors.Is(err, domain.ErrInvalidQuantity),
		errors.Is(err, domain.ErrInvalidMoney),
		errors.Is(err, domain.ErrInvalidCustomerID),
		errors.Is(err, domain.ErrInvalidOrderID),
		errors.Is(err, domain.ErrInvalidReservationRef),
		errors.Is(err, domain.ErrInvalidShipmentID),
		errors.Is(err, domain.ErrInvalidTrackingNumber):
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
