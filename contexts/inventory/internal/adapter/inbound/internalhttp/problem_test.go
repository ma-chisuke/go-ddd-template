package internalhttp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/example/go-ddd-template/contexts/inventory/internal/adapter/inbound/openapiinternal"
	"github.com/example/go-ddd-template/contexts/inventory/internal/domain/inventory"
	"github.com/example/go-ddd-template/shared/problem"
)

// このファイルは内部 API のエラー応答 4 経路（E1〜E4）を、実際にサーバへリクエストを
// 投げて検証する。内部 API は「明細の配列」を受け取る唯一のエンドポイント
// （POST /reservations）を持つため、明細位置の付与（Index 経路）もここで検証する。

// typeBase は type URI の名前空間。problem.go の problemTypeBase を書き換えたら
// この期待値だけを直せばよい。
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
	var typed openapiinternal.ProblemDetails
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

// reserveBody は予約要求の本文を組み立てる。
func reserveBody(ref string, lines ...string) string {
	return fmt.Sprintf(`{"ref":%q,"lines":[%s]}`, ref, strings.Join(lines, ","))
}

func reserveLine(sku string, qty int) string {
	return fmt.Sprintf(`{"sku":%q,"quantity":%d}`, sku, qty)
}

// ---------------------------------------------------------------------------
// E1: 契約検証（400 / 415）
// ---------------------------------------------------------------------------

func TestProblem_E1_ContractValidation(t *testing.T) {
	ts := newInternalServer(t)

	t.Run("境界: 必須欠落は兄弟フィールドを全件列挙する", func(t *testing.T) {
		pb := readProblem(t, post(t, ts.Client(), ts.URL+"/reservations", `{}`),
			http.StatusBadRequest, problem.TypeValidationError)

		names := make([]string, 0, len(pb.InvalidParams))
		for _, p := range pb.InvalidParams {
			names = append(names, p.Name)
			assert.Equal(t, problem.CodeRequired, p.Code)
		}
		assert.ElementsMatch(t, []string{"ref", "lines"}, names)
	})

	t.Run("異常系: 明細の中の型不一致は添字を付けない（規則 R-9）", func(t *testing.T) {
		pb := readProblem(t, post(t, ts.Client(), ts.URL+"/reservations",
			reserveBody("ORDER-1", `{"sku":"WIDGET-001","quantity":"three"}`)),
			http.StatusBadRequest, problem.TypeValidationError)

		require.Len(t, pb.InvalidParams, 1)
		assert.Equal(t, "lines.quantity", pb.InvalidParams[0].Name)
		assert.Equal(t, problem.CodeType, pb.InvalidParams[0].Code)
	})

	t.Run("異常系: 不正 JSON は invalid-params をキーごと省略する（規則 R-14）", func(t *testing.T) {
		pb := readProblem(t, post(t, ts.Client(), ts.URL+"/reservations", `{"ref":`),
			http.StatusBadRequest, problem.TypeValidationError)
		assert.Empty(t, pb.InvalidParams)
		assert.NotContains(t, pb.raw, "invalid-params")
	})

	t.Run("異常系: Content-Type 不正は 415", func(t *testing.T) {
		pb := readProblem(t, send(t, ts, http.MethodPost, "/reservations", "text/plain",
			reserveBody("ORDER-1", reserveLine("WIDGET-001", 1))),
			http.StatusUnsupportedMediaType, problem.TypeUnsupportedMediaType)
		assert.Empty(t, pb.InvalidParams)
	})
}

// ---------------------------------------------------------------------------
// E2 / E3
// ---------------------------------------------------------------------------

func TestProblem_E2_NotFoundIsProblemJSON(t *testing.T) {
	ts := newInternalServer(t)

	pb := readProblem(t, send(t, ts, http.MethodGet, "/no-such-endpoint", "", ""),
		http.StatusNotFound, problem.TypeNotFound)

	assert.NotContains(t, pb.raw, "404 page not found", "Go 標準のプレーンテキストが出ない")
	assert.Empty(t, pb.InvalidParams)
}

func TestProblem_E3_MethodNotAllowed(t *testing.T) {
	ts := newInternalServer(t)

	t.Run("異常系: 405 は problem+json で Allow ヘッダを維持する", func(t *testing.T) {
		resp := send(t, ts, http.MethodGet, "/reservations", "", "")
		allow := resp.Header.Get("Allow")
		readProblem(t, resp, http.StatusMethodNotAllowed, problem.TypeMethodNotAllowed)
		assert.Contains(t, allow, http.MethodPost, "Allow は本文書き出し前に設定される")
	})

	t.Run("正常系: OPTIONS は 405 にせず CORS プリフライトを壊さない", func(t *testing.T) {
		resp := send(t, ts, http.MethodOptions, "/reservations", "", "")
		require.NoError(t, resp.Body.Close())
		assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	})
}

// ---------------------------------------------------------------------------
// E4: ドメイン検証（422）
// ---------------------------------------------------------------------------

func TestProblem_E4_DomainValidation(t *testing.T) {
	ts := newInternalServer(t)

	cases := []struct {
		name     string
		path     string
		body     string
		wantName string
		wantCode string
	}{
		{
			name: "予約参照が空", path: "/reservations",
			body:     reserveBody("  ", reserveLine("WIDGET-001", 1)),
			wantName: "ref", wantCode: inventory.VReservationRef.Code,
		},
		{
			name: "明細の SKU が空（アプリ層のループが位置を付ける）", path: "/reservations",
			body:     reserveBody("ORDER-1", reserveLine("  ", 1)),
			wantName: "lines[0].sku", wantCode: inventory.VSKU.Code,
		},
		{
			name: "明細の数量が負（値オブジェクトで弾かれる）", path: "/reservations",
			body:     reserveBody("ORDER-1", reserveLine("WIDGET-001", -1)),
			wantName: "lines[0].quantity", wantCode: inventory.VQuantity.Code,
		},
		{
			name: "確定の参照が空（パスパラメータ）", path: "/reservations/%20/confirm",
			wantName: "ref", wantCode: inventory.VReservationRef.Code,
		},
		{
			name: "解放の参照が空（パスパラメータ）", path: "/reservations/%20/release",
			wantName: "ref", wantCode: inventory.VReservationRef.Code,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pb := readProblem(t, post(t, ts.Client(), ts.URL+tc.path, tc.body),
				http.StatusUnprocessableEntity, problem.TypeInvalidInput)

			require.Len(t, pb.InvalidParams, 1)
			assert.Equal(t, tc.wantName, pb.InvalidParams[0].Name)
			assert.Equal(t, tc.wantCode, pb.InvalidParams[0].Code)
			assert.NotEmpty(t, pb.InvalidParams[0].Reason)
		})
	}
}

// 数量 0 は在庫の値オブジェクト（>= 0）を通過し、ドメインサービス側で弾かれる。
// 明細の走査は集約側にあるため、位置はドメインの FieldViolation.Index が運ぶ。
// 常に [0] を返す壊れた実装で通らないよう、壊す行を動かして検証する。
func TestProblem_E4_ZeroQuantityCarriesLineIndexFromDomain(t *testing.T) {
	ts := newInternalServer(t)

	for _, broken := range []int{0, 1, 2} {
		t.Run(fmt.Sprintf("%d 行目が 0", broken), func(t *testing.T) {
			lines := []string{
				reserveLine("WIDGET-001", 1),
				reserveLine("WIDGET-001", 1),
				reserveLine("WIDGET-001", 1),
			}
			lines[broken] = reserveLine("WIDGET-001", 0)

			pb := readProblem(t, post(t, ts.Client(), ts.URL+"/reservations", reserveBody("ORDER-1", lines...)),
				http.StatusUnprocessableEntity, problem.TypeInvalidInput)

			require.Len(t, pb.InvalidParams, 1)
			assert.Equal(t, fmt.Sprintf("lines[%d].quantity", broken), pb.InvalidParams[0].Name)
			assert.Equal(t, inventory.VQuantity.Code, pb.InvalidParams[0].Code)
		})
	}
}

// NFR-4.2: 既存の NewError 経路の type 移行が実際に効いたことを固定する。
// あわせて FR-2.4（受信値のエコーバック禁止）の是正も検証する — この内部 API の
// 404 / 409 は SKU・要求数量・利用可能在庫をエラー文言に含んでいた。
func TestProblem_E4_TypeMigrationAndNoEcho(t *testing.T) {
	ts := newInternalServer(t)

	t.Run("契約: 予約が無い 404 は resource-not-found で参照をエコーしない", func(t *testing.T) {
		pb := readProblem(t, post(t, ts.Client(), ts.URL+"/reservations/SECRET-REF/confirm", ""),
			http.StatusNotFound, problem.TypeResourceNotFound)

		assert.Empty(t, pb.InvalidParams)
		assert.Equal(t, problem.DetailResourceNotFound, pb.Detail)
		assert.NotContains(t, pb.raw, "SECRET-REF", "受信値をエコーバックしない（FR-2.4）")
	})

	t.Run("異常系: 在庫項目が無い 404 は SKU をエコーしない", func(t *testing.T) {
		pb := readProblem(t, post(t, ts.Client(), ts.URL+"/reservations",
			reserveBody("ORDER-1", reserveLine("SECRET-SKU", 1))),
			http.StatusNotFound, problem.TypeResourceNotFound)

		assert.NotContains(t, pb.raw, "SECRET-SKU", "受信値をエコーバックしない（FR-2.4）")
	})

	t.Run("契約: 在庫不足の 409 は conflict で数量・在庫数をエコーしない", func(t *testing.T) {
		pb := readProblem(t, post(t, ts.Client(), ts.URL+"/reservations",
			reserveBody("ORDER-1", reserveLine("WIDGET-001", 999))),
			http.StatusConflict, problem.TypeConflict)

		assert.Empty(t, pb.InvalidParams, "在庫不足は入力フィールドに帰着しない（規則 R-14）")
		assert.Equal(t, problem.DetailConflict, pb.Detail)
		assert.NotContains(t, pb.raw, "999", "要求数量を漏らさない")
		assert.NotContains(t, pb.raw, "WIDGET-001", "SKU を漏らさない")
	})

	t.Run("契約: 未登録のメッセージ種別の 422 は invalid-input", func(t *testing.T) {
		pb := readProblem(t, post(t, ts.Client(), ts.URL+"/events", `{"id":"m-1","type":"unknown.type","payload":"{}"}`),
			http.StatusUnprocessableEntity, problem.TypeInvalidInput)
		assert.Empty(t, pb.InvalidParams, "配送ルート未登録はフィールドに帰着しない")
	})
}

// TestProblem_InvalidParamCodeEnumCoversVocabulary は、この内部 API のサーバが応答に
// 載せうる code 語彙のすべてが、契約（internal.openapi.yaml）の InvalidParam.code enum に
// 含まれることを網羅的に検証する。列挙元は語彙の唯一の情報源そのもの——契約検証語彙
// （shared/problem の Code*）と、在庫コンテキストが所有するドメイン検証語彙
// （inventory.Rule の Code）——である。
//
// readProblem 内の Validate はテストが実際に踏んだ経路の code しか検証できない。この網羅
// テストは経路に依存せず、語彙 → enum の対応を直接固定する。新しい inventory.Rule を足したのに
// 契約の enum へ足し忘れれば、その code の生成型 Validate が invalid value を返し CI が落ちる
// （規則 R-19）。
func TestProblem_InvalidParamCodeEnumCoversVocabulary(t *testing.T) {
	// 契約検証語彙（400 / validation-error）。shared/problem/vocab.go の Code* が唯一の情報源。
	contractCodes := []string{
		problem.CodeRequired, problem.CodeType, problem.CodeMinLength, problem.CodeMaxLength,
		problem.CodePattern, problem.CodeUniqueItems, problem.CodeInvalidParam,
		problem.CodeBodyRequired, problem.CodeInvalid,
	}
	// ドメイン検証語彙（422 / invalid-input）。在庫コンテキストの inventory.Rule が唯一の情報源。
	domainCodes := []string{
		inventory.VSKU.Code, inventory.VQuantity.Code, inventory.VReservationRef.Code,
	}

	for _, code := range append(contractCodes, domainCodes...) {
		assert.NoErrorf(t, openapiinternal.InvalidParamCode(code).Validate(),
			"code %q は契約（internal.openapi.yaml）の InvalidParam.code enum に含まれる必要がある", code)
	}
}
