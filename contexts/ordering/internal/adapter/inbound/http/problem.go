package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"unicode"

	"github.com/example/go-ddd-template/contexts/ordering/internal/adapter/inbound/openapi"
	"github.com/example/go-ddd-template/contexts/ordering/internal/application"
	"github.com/example/go-ddd-template/contexts/ordering/internal/domain/order"
	"github.com/example/go-ddd-template/shared/problem"
	"github.com/example/go-ddd-template/shared/problem/ogenproblem"
)

// このファイルは、このコンテキストの HTTP エラー応答のうち **コンテキスト固有の部分** と、
// ogen サーバのエラー経路（E1〜E3）を受け持つ。エラー応答を生成する経路は 4 つある。
//
//	E1 リクエストのデコード／契約検証   ogen ErrorHandler       -> errorHandler
//	E2 ルーティング不一致               ogen NotFound           -> notFoundHandler
//	E3 メソッド不許可                   ogen MethodNotAllowed   -> methodNotAllowedHandler
//	E4 ハンドラ戻り値のエラー           生成された NewError     -> errmap.go
//
// E1〜E3 は [Handler.ServerOptions] で ogen サーバへ注入する。注入しないと ogen の既定
// ハンドラが `{"error_message": "operation placeOrder: decode request: ..."}` を返し、
// 内部実装の詳細が外へ漏れる（FR-1 / FR-2 / NFR-1）。
//
// 共通化できる部分（種別 URI 台帳・contract 検証の語彙・パス表記・ogen からの抽出）は
// shared/problem と shared/problem/ogenproblem にある。ここに残るのは 2 つだけ:
//
//	1. ドメイン検証（422）の語彙と reason 表 — コンテキストが所有する（制約 C-6 / 規則 R-7）
//	2. ProblemDetails の組み立て — 生成型は契約ごとに別の Go 型なので shared では跨げない

// problemTypeBase は type URI の名前空間。
//
// **テンプレート利用者はこの 1 箇所を自分の名前空間へ書き換える**（FR-5.3）。URI は
// 識別子であり、解決可能な文書ページを公開する必要はない（OOS-5）。手順は CONVENTIONS.md。
const problemTypeBase = "https://github.com/example/go-ddd-template/problems/"

// domainReasons はドメインの検証規則（order.Rule）に対応する人間可読な定型文。
//
// **キーを Rule から引いているので、code の綴りがドメインの一覧とずれることが構造的に
// 起こらない。** ドメインに規則を 1 つ足したときに編集するのはドメインの Rule 一覧と
// この表の 2 箇所だけである（規則 R-19）。
//
// この表は「注文」コンテキストが所有する。在庫コンテキストにも同名の code があるが、
// 意味が違うので共有しない（制約 C-6 / 規則 R-7）。invalid_quantity が典型で、注文行は
// 1 以上、在庫は 0 以上である。値域の違いはこの文言の違いとして現れる。
//
// 受信値も閾値の由来も載せない — 定型文だけを返す（FR-2.3 / FR-2.4）。
var domainReasons = map[string]string{
	order.VEmptyOrder.Code:     "1 行以上の明細を指定してください",
	order.VSKU.Code:            "SKU を指定してください",
	order.VQuantity.Code:       "1 以上の値を指定してください",
	order.VMoneyAmount.Code:    "0 以上の値を指定してください",
	order.VMoneyCurrency.Code:  "通貨コードを指定してください",
	order.VCustomerID.Code:     "顧客 ID を指定してください",
	order.VOrderID.Code:        "注文 ID を指定してください",
	order.VReservationRef.Code: "予約参照を指定してください",
}

// domainReasonOf はドメイン検証 code に対応する定型文を返す。
// 表に無い code は汎用文言へフォールバックする。code 自体は応答に載るので、
// クライアントは code で分岐でき情報は失われない。
func domainReasonOf(code string) string {
	if r, ok := domainReasons[code]; ok {
		return r
	}
	return "値が不正です"
}

// jsonNames はアプリケーション層のパス断片（Go / DTO の識別子）を JSON / パラメータの
// 名前へ写す **上書き表**。機械的な変換（先頭 1 文字を小文字にする）で決まらないものだけを書く。
//
//	Quantity -> quantity   … 表に無くても自動で正しくなる
//	Sku      -> sku        … 同上
//	OrderId  -> id         … 名前そのものが違う（パスパラメータ /orders/{id}）ので表が要る
//
// Go の識別子を応答に露出させてはならない（規則 R-10）。新しいフィールドを足したときに
// 自動変換で正しくならないなら、ここに 1 行足す。
var jsonNames = map[string]string{
	"OrderId": "id",
}

// Handler.ServerOptions は ogen サーバへ渡すオプション一式を返す。
//
// **本番の合成ルートもテストもこのメソッド経由でサーバを組み立てる。** 片方だけが
// オプションを渡す状態になると、テストが通っても本番では ogen の既定ハンドラが出る、
// という最悪の食い違いが起きる（NFR-6）。
func (h *Handler) ServerOptions() []openapi.ServerOption {
	return []openapi.ServerOption{
		openapi.WithErrorHandler(h.errorHandler),
		openapi.WithNotFound(h.notFoundHandler),
		openapi.WithMethodNotAllowed(h.methodNotAllowedHandler),
	}
}

// errorHandler は E1（デコード／契約検証の失敗）を problem+json へ翻訳する。
//
// ステータスは ogen の判定をそのまま尊重し、独自の再割り当てはしない（FR-1.5）。
// 元のエラーはログにだけ残す（FR-2.5 / 規則 R-13）。相関 ID（CorrelationMiddleware）と
// 突き合わせれば運用者は元の文言まで辿れる。
func (h *Handler) errorHandler(ctx context.Context, w http.ResponseWriter, _ *http.Request, err error) {
	status := ogenproblem.StatusOf(err)

	var suffix, detail string
	switch {
	case status == http.StatusUnsupportedMediaType:
		suffix, detail = problem.TypeUnsupportedMediaType, problem.DetailUnsupportedMedia
	case status >= http.StatusInternalServerError:
		suffix, detail = problem.TypeInternalError, problem.DetailInternalError
	default:
		suffix, detail = problem.TypeValidationError, problem.DetailValidationError
	}

	h.logProblem(ctx, status, err)
	h.writeProblem(ctx, w, status, newProblem(status, suffix, detail, contractParams(err)))
}

// notFoundHandler は E2（ルーティング不一致）を problem+json へ翻訳する。
//
// ここは「そのようなエンドポイントは無い」であり、E4 の「エンドポイントはあるが対象
// リソースが無い」（resource-not-found）とは別の種別を与える（規則 R-2）。同じ 404 でも
// クライアントの取るべき行動は違う — 前者は URL の誤り、後者は ID の誤りである。
func (h *Handler) notFoundHandler(w http.ResponseWriter, r *http.Request) {
	h.writeProblem(r.Context(), w, http.StatusNotFound,
		newProblem(http.StatusNotFound, problem.TypeNotFound, problem.DetailNotFound, nil))
}

// methodNotAllowedHandler は E3（メソッド不許可）を problem+json へ翻訳する。
//
// 2 点の注意がある。
//   - Allow ヘッダは **本文を書き出す前に** 設定する。WriteHeader のあとにヘッダを
//     変更しても送出済みなので効かない。
//   - OPTIONS は 405 にしない。ogen の既定実装は OPTIONS を CORS プリフライトとして
//     204 で返しており（CORS ヘッダはこの関数が呼ばれる前に設定済み）、ここで 405 を
//     返すとプリフライトを壊す。オプションを注入したせいで既存の振る舞いを壊さない。
func (h *Handler) methodNotAllowedHandler(w http.ResponseWriter, r *http.Request, allowed string) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("Allow", allowed)
	h.writeProblem(r.Context(), w, http.StatusMethodNotAllowed,
		newProblem(http.StatusMethodNotAllowed, problem.TypeMethodNotAllowed, problem.DetailMethodNotAllowed, nil))
}

// logProblem は排除した情報をログへ残す（規則 R-13）。4xx は Warn、5xx は Error。
func (h *Handler) logProblem(ctx context.Context, status int, err error) {
	if status >= http.StatusInternalServerError {
		h.log.ErrorContext(ctx, "内部エラーが発生しました", "status", status, "error", err)
		return
	}
	h.log.WarnContext(ctx, "リクエストを処理できませんでした", "status", status, "error", err)
}

// writeProblem は ProblemDetails を application/problem+json として書き出す。
// 手書き JSON ではなく生成型を符号化する（FR-6.2）。
func (h *Handler) writeProblem(ctx context.Context, w http.ResponseWriter, status int, pd openapi.ProblemDetails) {
	// 生成型の MarshalJSON はポインタレシーバなので、必ずアドレスを渡す
	// （値のまま渡すと標準の構造体符号化になり、拡張メンバーの JSON 名がずれる）。
	body, err := json.Marshal(&pd)
	if err != nil {
		// 生成型の符号化が失敗するのは想定外。本文なしの 500 に留めて事実をログへ残す。
		h.log.ErrorContext(ctx, "エラー応答の符号化に失敗しました", "error", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// newProblem は自コンテキストの生成型で ProblemDetails を組み立てる。
// params が空なら invalid-params はキーごと省略される（生成コードが nil を書き出さない。
// 規則 R-14 / FR-3.4）。
func newProblem(status int, typeSuffix, detail string, params []openapi.InvalidParam) openapi.ProblemDetails {
	return openapi.ProblemDetails{
		Type:          problemTypeOf(typeSuffix),
		Title:         problem.TitleOf(typeSuffix, http.StatusText(status)),
		Status:        status,
		Detail:        openapi.NewOptString(detail),
		InvalidParams: params,
	}
}

// problemTypeOf は種別サフィックスから type URI を組み立てる。
func problemTypeOf(suffix string) url.URL {
	return mustParseURL(problemTypeBase + suffix)
}

// contractParams は ogen のエラー（E1）から invalid-params を組み立てる。
// 語彙は契約検証語彙なので reason は shared から引く（規則 R-5）。
func contractParams(err error) []openapi.InvalidParam {
	params := ogenproblem.ExtractParams(err)
	if len(params) == 0 {
		return nil
	}
	out := make([]openapi.InvalidParam, 0, len(params))
	for _, p := range params {
		out = append(out, openapi.InvalidParam{
			Name: p.Name,
			// code は契約で enum 化されており、生成型 InvalidParamCode（string の別名）になる。
			// ドメイン／アプリ層はプレーンな文字列のまま扱い、この境界（インターフェース層）で
			// enum 型へ写す。綴りが契約の enum から外れれば生成型の Validate が弾く。
			Code:   openapi.InvalidParamCode(p.Code),
			Reason: openapi.NewOptString(problem.ReasonOf(p.Code)),
		})
	}
	return out
}

// domainParams はアプリケーション層の ValidationError（E4）から invalid-params を
// 組み立てる。語彙はドメイン検証語彙なので reason はこのファイルの表から引く（規則 R-5）。
func domainParams(err error) []openapi.InvalidParam {
	var ve *application.ValidationError
	if !errors.As(err, &ve) || len(ve.Violations) == 0 {
		return nil
	}
	out := make([]openapi.InvalidParam, 0, len(ve.Violations))
	for _, v := range ve.Violations {
		out = append(out, openapi.InvalidParam{
			Name: toJSONPath(v.Path),
			// ドメイン検証 code も契約の enum に含める（規則 R-19）。境界で enum 型へ写す。
			Code:   openapi.InvalidParamCode(v.Code),
			Reason: openapi.NewOptString(domainReasonOf(v.Code)),
		})
	}
	return out
}

// toJSONPath はアプリケーション層のパス（"Lines[0].UnitPrice.Amount"）を
// HTTP のフィールドパス（"lines[0].unitPrice.amount"）へ翻訳する（FR-4.6 / 規則 R-10）。
//
// 添字の記法（規則 R-8）は shared の problem.JoinPath が持つ。ここが持つのは
// 「Go / DTO の名前 → JSON の名前」の写像だけである。
func toJSONPath(path string) string {
	segs := strings.Split(path, ".")
	out := make([]string, 0, len(segs)*2)
	for _, s := range segs {
		name, index := splitIndex(s)
		out = append(out, jsonNameOf(name))
		if index != "" {
			out = append(out, index)
		}
	}
	return problem.JoinPath(out)
}

// jsonNameOf は 1 つのパス断片を JSON の名前へ写す。
func jsonNameOf(name string) string {
	if mapped, ok := jsonNames[name]; ok {
		return mapped
	}
	return lowerFirst(name)
}

// splitIndex は "Lines[0]" を ("Lines", "[0]") に分ける。添字が無ければ第 2 戻り値は空。
func splitIndex(seg string) (name, index string) {
	if i := strings.IndexByte(seg, '['); i >= 0 {
		return seg[:i], seg[i:]
	}
	return seg, ""
}

// lowerFirst は先頭 1 文字を小文字にする（Quantity -> quantity）。
func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	r[0] = unicode.ToLower(r[0])
	return string(r)
}
