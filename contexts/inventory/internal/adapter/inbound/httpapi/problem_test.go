package httpapi_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/example/go-ddd-template/contexts/inventory/internal/adapter/inbound/openapi"
	"github.com/example/go-ddd-template/contexts/inventory/internal/domain"
	"github.com/example/go-ddd-template/shared/problem"
)

// このファイルは公開 API のエラー応答 4 経路（E1〜E4）を、実際にサーバへリクエストを
// 投げて検証する。ハンドラ関数を直接呼ぶのではなく ogen サーバを通すのが要点である
// （サーバオプションの渡し忘れはハンドラ単体テストでは検出できない）。

// typeBase は type URI の名前空間。problem.go の problemTypeBase を書き換えたら
// この期待値だけを直せばよい（差し替え箇所が 1 つであることをテストが示す）。
const typeBase = "https://github.com/example/go-ddd-template/problems/"

// ogenLeaks は応答本文に絶対に現れてはならない文字列（規則 R-11 / NFR-1）。
var ogenLeaks = []string{
	"operation ", "decode request", "decode params", "callback:", "unexpected byte",
	"field required", "openapi", "ogen", ".go:",
}

type problemBody struct {
	Type          string         `json:"type"`
	Title         string         `json:"title"`
	Status        int            `json:"status"`
	Detail        string         `json:"detail"`
	InvalidParams []invalidParam `json:"invalid-params"`
	raw           string
}

type invalidParam struct {
	Name   string `json:"name"`
	Code   string `json:"code"`
	Reason string `json:"reason"`
}

func readProblem(t *testing.T, resp *http.Response, wantStatus int, wantTypeSuffix string) problemBody {
	t.Helper()
	defer func() { require.NoError(t, resp.Body.Close()) }()

	require.Equal(t, wantStatus, resp.StatusCode, "HTTP ステータス")
	assert.Equal(t, "application/problem+json", resp.Header.Get("Content-Type"), "Content-Type")

	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err, "本文の読み取り")

	var pb problemBody
	require.NoError(t, json.Unmarshal(raw, &pb), "problem+json のデコード: %s", raw)
	pb.raw = string(raw)

	// 生成型（ogen）へも復号して Validate を通す。応答の code は契約で enum 化されており、
	// ProblemDetails.Validate が invalid-params[].code を enum と突き合わせる。readProblem を
	// 通る全経路（E1〜E4）のテストがこれを実行するので、サーバが enum 外の code を載せた瞬間に
	// CI が落ちる（契約の enum と実装の語彙が乖離しない保証）。
	var typed openapi.ProblemDetails
	require.NoError(t, json.Unmarshal(raw, &typed), "生成型 ProblemDetails への復号: %s", raw)
	assert.NoError(t, typed.Validate(), "生成型 Validate（code enum を含む）: %s", raw)

	assert.Equal(t, wantStatus, pb.Status, "problem.status")
	assert.Equal(t, typeBase+wantTypeSuffix, pb.Type, "problem.type")
	assert.NotEqual(t, "about:blank", pb.Type, "type は about:blank ではない（FR-5.1）")
	assert.NotEmpty(t, pb.Title, "problem.title")
	assert.NotEmpty(t, pb.Detail, "problem.detail")

	for _, leak := range ogenLeaks {
		assert.NotContains(t, pb.raw, leak, "ogen / Go 由来の文言が漏れている")
	}
	return pb
}

func send(t *testing.T, ts *httptest.Server, method, path, contentType, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, ts.URL+path, strings.NewReader(body))
	require.NoError(t, err, "リクエスト生成")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := ts.Client().Do(req)
	require.NoError(t, err, "送信")
	return resp
}

// ---------------------------------------------------------------------------
// E1: 契約検証（400 / 415）
// ---------------------------------------------------------------------------

func TestProblem_E1_ContractValidation(t *testing.T) {
	ts := newServer(t)

	t.Run("境界: 必須欠落は兄弟フィールドを全件列挙する", func(t *testing.T) {
		pb := readProblem(t, postJSON(t, ts.Client(), ts.URL+"/stock/WIDGET-001/replenish", `{}`),
			http.StatusBadRequest, problem.TypeValidationError)

		require.Len(t, pb.InvalidParams, 1)
		assert.Equal(t, "quantity", pb.InvalidParams[0].Name)
		assert.Equal(t, problem.CodeRequired, pb.InvalidParams[0].Code)
		assert.Equal(t, problem.ReasonOf(problem.CodeRequired), pb.InvalidParams[0].Reason)
	})

	t.Run("異常系: 型不一致はラップ列からパスを組む", func(t *testing.T) {
		pb := readProblem(t, postJSON(t, ts.Client(), ts.URL+"/stock/WIDGET-001/replenish", `{"quantity":"ten"}`),
			http.StatusBadRequest, problem.TypeValidationError)

		require.Len(t, pb.InvalidParams, 1)
		assert.Equal(t, "quantity", pb.InvalidParams[0].Name)
		assert.Equal(t, problem.CodeType, pb.InvalidParams[0].Code)
	})

	t.Run("異常系: 不正 JSON は invalid-params をキーごと省略する（規則 R-14）", func(t *testing.T) {
		pb := readProblem(t, postJSON(t, ts.Client(), ts.URL+"/stock/WIDGET-001/replenish", `{"quantity":`),
			http.StatusBadRequest, problem.TypeValidationError)

		assert.Empty(t, pb.InvalidParams)
		assert.NotContains(t, pb.raw, "invalid-params")
	})

	t.Run("異常系: Content-Type 不正は 415", func(t *testing.T) {
		pb := readProblem(t, send(t, ts, http.MethodPost, "/stock/WIDGET-001/replenish", "text/plain", `{"quantity":1}`),
			http.StatusUnsupportedMediaType, problem.TypeUnsupportedMediaType)
		assert.Empty(t, pb.InvalidParams)
	})
}

// ---------------------------------------------------------------------------
// E2 / E3
// ---------------------------------------------------------------------------

func TestProblem_E2_NotFoundIsProblemJSON(t *testing.T) {
	ts := newServer(t)

	pb := readProblem(t, send(t, ts, http.MethodGet, "/no-such-endpoint", "", ""),
		http.StatusNotFound, problem.TypeNotFound)

	assert.NotContains(t, pb.raw, "404 page not found", "Go 標準のプレーンテキストが出ない")
	assert.Empty(t, pb.InvalidParams)
}

func TestProblem_E3_MethodNotAllowed(t *testing.T) {
	ts := newServer(t)

	t.Run("異常系: 405 は problem+json で Allow ヘッダを維持する", func(t *testing.T) {
		resp := send(t, ts, http.MethodDelete, "/stock/WIDGET-001", "", "")
		allow := resp.Header.Get("Allow")
		readProblem(t, resp, http.StatusMethodNotAllowed, problem.TypeMethodNotAllowed)
		assert.Contains(t, allow, http.MethodGet, "Allow は本文書き出し前に設定される")
	})

	t.Run("正常系: OPTIONS は 405 にせず CORS プリフライトを壊さない", func(t *testing.T) {
		resp := send(t, ts, http.MethodOptions, "/stock/WIDGET-001", "", "")
		require.NoError(t, resp.Body.Close())
		assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	})
}

// ---------------------------------------------------------------------------
// E4: ドメイン検証（422）と type 移行
// ---------------------------------------------------------------------------

func TestProblem_E4_DomainValidation(t *testing.T) {
	ts := newServer(t)

	t.Run("境界: 補充数量 0 は quantity を指す（値オブジェクトを通過し集約で弾かれる）", func(t *testing.T) {
		pb := readProblem(t, postJSON(t, ts.Client(), ts.URL+"/stock/WIDGET-001/replenish", `{"quantity":0}`),
			http.StatusUnprocessableEntity, problem.TypeInvalidInput)

		require.Len(t, pb.InvalidParams, 1)
		assert.Equal(t, "quantity", pb.InvalidParams[0].Name)
		assert.Equal(t, domain.VQuantity.Code, pb.InvalidParams[0].Code)
		assert.NotEmpty(t, pb.InvalidParams[0].Reason)
	})

	t.Run("境界: 補充数量が負も quantity を指す（値オブジェクトで弾かれる）", func(t *testing.T) {
		pb := readProblem(t, postJSON(t, ts.Client(), ts.URL+"/stock/WIDGET-001/replenish", `{"quantity":-1}`),
			http.StatusUnprocessableEntity, problem.TypeInvalidInput)

		require.Len(t, pb.InvalidParams, 1)
		assert.Equal(t, "quantity", pb.InvalidParams[0].Name)
	})

	t.Run("境界: 空の SKU パスパラメータは sku を指す", func(t *testing.T) {
		pb := readProblem(t, send(t, ts, http.MethodGet, "/stock/%20", "", ""),
			http.StatusUnprocessableEntity, problem.TypeInvalidInput)

		require.Len(t, pb.InvalidParams, 1)
		assert.Equal(t, "sku", pb.InvalidParams[0].Name, "Go 識別子 SKU を露出しない（規則 R-10）")
		assert.Equal(t, domain.VSKU.Code, pb.InvalidParams[0].Code)
	})
}

// NFR-4.2: 既存の NewError 経路の type 移行が実際に効いたことを固定する。
func TestProblem_E4_TypeMigration(t *testing.T) {
	ts := newServer(t)

	t.Run("契約: 404 は resource-not-found で E2 の not-found とは別種別", func(t *testing.T) {
		pb := readProblem(t, send(t, ts, http.MethodGet, "/stock/MISSING", "", ""),
			http.StatusNotFound, problem.TypeResourceNotFound)
		assert.Empty(t, pb.InvalidParams)
		assert.Equal(t, problem.DetailResourceNotFound, pb.Detail)
	})

	t.Run("異常系: 404 の detail に SKU が漏れない（FR-2.4）", func(t *testing.T) {
		pb := readProblem(t, send(t, ts, http.MethodGet, "/stock/SECRET-SKU", "", ""),
			http.StatusNotFound, problem.TypeResourceNotFound)
		assert.NotContains(t, pb.raw, "SECRET-SKU", "受信値をエコーバックしない")
	})

	t.Run("契約: 422 は invalid-input", func(t *testing.T) {
		readProblem(t, postJSON(t, ts.Client(), ts.URL+"/stock/WIDGET-001/replenish", `{"quantity":0}`),
			http.StatusUnprocessableEntity, problem.TypeInvalidInput)
	})
}

// E2 の 404 と E4 の 404 が同じ status で違う type を持つこと（規則 R-2）。
func TestProblem_SameStatusDifferentType(t *testing.T) {
	ts := newServer(t)

	routing := readProblem(t, send(t, ts, http.MethodGet, "/no-such-endpoint", "", ""),
		http.StatusNotFound, problem.TypeNotFound)
	resource := readProblem(t, send(t, ts, http.MethodGet, "/stock/MISSING", "", ""),
		http.StatusNotFound, problem.TypeResourceNotFound)

	assert.Equal(t, routing.Status, resource.Status, "status は同じ 404")
	assert.NotEqual(t, routing.Type, resource.Type, "URL の誤りと ID の誤りは type で区別できる")
	assert.NotEqual(t, routing.Title, resource.Title, "title も type と 1 対 1（規則 R-3）")
	assert.NotEqual(t, routing.Detail, resource.Detail)
}

// TestProblem_InvalidParamCodeEnumCoversVocabulary は、このサーバが応答に載せうる code
// 語彙のすべてが、契約（openapi.yaml）の InvalidParam.code enum に含まれることを網羅的に
// 検証する。列挙元は語彙の唯一の情報源そのもの——契約検証語彙（shared/problem の Code*）と、
// 在庫コンテキストが所有するドメイン検証語彙（domain.Rule の Code）——である。
//
// readProblem 内の Validate はテストが実際に踏んだ経路の code しか検証できない。この網羅
// テストは経路に依存せず、語彙 → enum の対応を直接固定する。新しい domain.Rule を足したのに
// 契約の enum へ足し忘れれば、その code の生成型 Validate が invalid value を返し CI が落ちる
// （規則 R-19）。
func TestProblem_InvalidParamCodeEnumCoversVocabulary(t *testing.T) {
	// 契約検証語彙（400 / validation-error）。shared/problem/vocab.go の Code* が唯一の情報源。
	contractCodes := []string{
		problem.CodeRequired, problem.CodeType, problem.CodeMinLength, problem.CodeMaxLength,
		problem.CodePattern, problem.CodeUniqueItems, problem.CodeInvalidParam,
		problem.CodeBodyRequired, problem.CodeInvalid,
	}
	// ドメイン検証語彙（422 / invalid-input）。在庫コンテキストの domain.Rule が唯一の情報源。
	domainCodes := []string{
		domain.VSKU.Code, domain.VQuantity.Code, domain.VReservationRef.Code,
	}

	for _, code := range append(contractCodes, domainCodes...) {
		assert.NoErrorf(t, openapi.InvalidParamCode(code).Validate(),
			"code %q は契約（openapi.yaml）の InvalidParam.code enum に含まれる必要がある", code)
	}
}
