package internalhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"unicode"

	"github.com/example/go-ddd-template/contexts/inventory/internal/adapter/inbound/openapiinternal"
	"github.com/example/go-ddd-template/contexts/inventory/internal/application"
	"github.com/example/go-ddd-template/contexts/inventory/internal/domain/inventory"
	"github.com/example/go-ddd-template/shared/problem"
	"github.com/example/go-ddd-template/shared/problem/ogenproblem"
)

// このファイルは、このコンテキストの内部 API のエラー応答のうち **コンテキスト固有の部分**
// と、ogen サーバのエラー経路（E1〜E3）を受け持つ。エラー応答を生成する経路は 4 つある。
//
//	E1 リクエストのデコード／契約検証   ogen ErrorHandler       -> errorHandler
//	E2 ルーティング不一致               ogen NotFound           -> notFoundHandler
//	E3 メソッド不許可                   ogen MethodNotAllowed   -> methodNotAllowedHandler
//	E4 ハンドラ戻り値のエラー           生成された NewError     -> errmap.go
//
// 共通化できる部分（種別 URI 台帳・契約検証の語彙・パス表記・ogen からの抽出）は
// shared/problem と shared/problem/ogenproblem にある。ここに残るのはドメイン検証（422）の
// reason 表と、自コンテキストの生成型による ProblemDetails の組み立てだけである。

// problemTypeBase は type URI の名前空間。
//
// **テンプレート利用者はこの 1 箇所を自分の名前空間へ書き換える**（FR-5.3）。URI は
// 識別子であり、解決可能な文書ページを公開する必要はない（OOS-5）。手順は CONVENTIONS.md。
const problemTypeBase = "https://github.com/example/go-ddd-template/problems/"

// domainReasons はドメインの検証規則（inventory.Rule）に対応する人間可読な定型文。
//
// **キーを Rule から引いているので、code の綴りがドメインの一覧とずれることが構造的に
// 起こらない。** ドメインに規則を 1 つ足したときに編集するのはドメインの Rule 一覧と
// この表の 2 箇所だけである（規則 R-19）。
//
// この表は「在庫」コンテキストが所有する。注文コンテキストにも同名の code があるが、
// invalid_quantity の値域が違う（在庫は 0 以上、注文行は 1 以上）。共有しないのは
// この違いを文言に出すためである（制約 C-6 / 規則 R-7）。
var domainReasons = map[string]string{
	inventory.VSKU.Code:            "SKU を指定してください",
	inventory.VQuantity.Code:       "0 以上の値を指定してください（補充・予約は 1 以上）",
	inventory.VReservationRef.Code: "予約参照を指定してください",
}

// domainReasonOf はドメイン検証 code に対応する定型文を返す。
// 表に無い code は汎用文言へフォールバックする（code 自体は応答に載る）。
func domainReasonOf(code string) string {
	if r, ok := domainReasons[code]; ok {
		return r
	}
	return "値が不正です"
}

// jsonNames はアプリケーション層のパス断片（Go / DTO の識別子）を JSON / パラメータの
// 名前へ写す **上書き表**。機械的な変換（先頭 1 文字を小文字にする）で決まらないものだけを書く。
// 現状はすべて機械的な変換で正しくなる（Sku -> sku、Quantity -> quantity、Ref -> ref）ので
// 空である。必要になったら 1 行足す。
//
// Go の識別子を応答に露出させてはならない（規則 R-10）。
var jsonNames = map[string]string{}

// Handler.ServerOptions は ogen サーバへ渡すオプション一式を返す。
// 本番の合成ルートもテストもこのメソッド経由でサーバを組み立てる（NFR-6）。
func (h *Handler) ServerOptions() []openapiinternal.ServerOption {
	return []openapiinternal.ServerOption{
		openapiinternal.WithErrorHandler(h.errorHandler),
		openapiinternal.WithNotFound(h.notFoundHandler),
		openapiinternal.WithMethodNotAllowed(h.methodNotAllowedHandler),
	}
}

// errorHandler は E1（デコード／契約検証の失敗）を problem+json へ翻訳する。
// ステータスは ogen の判定をそのまま尊重する（FR-1.5）。元のエラーはログにだけ残す。
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
// 「そのようなエンドポイントは無い」であり、E4 の resource-not-found とは別種別（規則 R-2）。
func (h *Handler) notFoundHandler(w http.ResponseWriter, r *http.Request) {
	h.writeProblem(r.Context(), w, http.StatusNotFound,
		newProblem(http.StatusNotFound, problem.TypeNotFound, problem.DetailNotFound, nil))
}

// methodNotAllowedHandler は E3（メソッド不許可）を problem+json へ翻訳する。
//
//   - Allow ヘッダは本文を書き出す前に設定する（WriteHeader 後の変更は効かない）。
//   - OPTIONS は 405 にしない。ogen の既定実装は OPTIONS を CORS プリフライトとして
//     204 で返しており、ここで 405 にするとプリフライトを壊す。
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
func (h *Handler) writeProblem(ctx context.Context, w http.ResponseWriter, status int, pd openapiinternal.ProblemDetails) {
	// 生成型の MarshalJSON はポインタレシーバなので、必ずアドレスを渡す。
	body, err := json.Marshal(&pd)
	if err != nil {
		h.log.ErrorContext(ctx, "エラー応答の符号化に失敗しました", "error", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// newProblem は自コンテキストの生成型で ProblemDetails を組み立てる。
// params が空なら invalid-params はキーごと省略される（規則 R-14 / FR-3.4）。
func newProblem(status int, typeSuffix, detail string, params []openapiinternal.InvalidParam) openapiinternal.ProblemDetails {
	return openapiinternal.ProblemDetails{
		Type:          problemTypeOf(typeSuffix),
		Title:         problem.TitleOf(typeSuffix, http.StatusText(status)),
		Status:        status,
		Detail:        openapiinternal.NewOptString(detail),
		InvalidParams: params,
	}
}

// problemTypeOf は種別サフィックスから type URI を組み立てる。
func problemTypeOf(suffix string) url.URL {
	return mustParseURL(problemTypeBase + suffix)
}

// contractParams は ogen のエラー（E1）から invalid-params を組み立てる。
// 語彙は契約検証語彙なので reason は shared から引く（規則 R-5）。
func contractParams(err error) []openapiinternal.InvalidParam {
	params := ogenproblem.ExtractParams(err)
	if len(params) == 0 {
		return nil
	}
	out := make([]openapiinternal.InvalidParam, 0, len(params))
	for _, p := range params {
		out = append(out, openapiinternal.InvalidParam{
			Name: p.Name,
			// code は契約で enum 化されており、生成型 InvalidParamCode（string の別名）になる。
			// ドメイン／アプリ層はプレーンな文字列のまま扱い、この境界（インターフェース層）で
			// enum 型へ写す。綴りが契約の enum から外れれば生成型の Validate が弾く。
			Code:   openapiinternal.InvalidParamCode(p.Code),
			Reason: openapiinternal.NewOptString(problem.ReasonOf(p.Code)),
		})
	}
	return out
}

// domainParams はアプリケーション層の ValidationError（E4）から invalid-params を
// 組み立てる。語彙はドメイン検証語彙なので reason はこのファイルの表から引く（規則 R-5）。
func domainParams(err error) []openapiinternal.InvalidParam {
	var ve *application.ValidationError
	if !errors.As(err, &ve) || len(ve.Violations) == 0 {
		return nil
	}
	out := make([]openapiinternal.InvalidParam, 0, len(ve.Violations))
	for _, v := range ve.Violations {
		out = append(out, openapiinternal.InvalidParam{
			Name: toJSONPath(v.Path),
			// ドメイン検証 code も契約の enum に含める（規則 R-19）。境界で enum 型へ写す。
			Code:   openapiinternal.InvalidParamCode(v.Code),
			Reason: openapiinternal.NewOptString(domainReasonOf(v.Code)),
		})
	}
	return out
}

// toJSONPath はアプリケーション層のパス（"Lines[0].Quantity"）を
// HTTP のフィールドパス（"lines[0].quantity"）へ翻訳する（FR-4.6 / 規則 R-10）。
// 添字の記法（規則 R-8）は shared の problem.JoinPath が持つ。
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
