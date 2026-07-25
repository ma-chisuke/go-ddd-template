package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/example/go-ddd-template/contexts/ordering/internal/adapter/inbound/openapi"
	"github.com/example/go-ddd-template/contexts/ordering/internal/application"
	"github.com/example/go-ddd-template/contexts/ordering/internal/domain/order"
	"github.com/example/go-ddd-template/shared/problem"
)

// このファイルは HTTP エラー応答の 4 経路（E1〜E4）を、実際にサーバへリクエストを投げて
// 検証する。ハンドラ関数を直接呼ぶのではなく ogen サーバを通すのが要点である。
// サーバオプションを渡し忘れると ogen の既定ハンドラが出るが、ハンドラ関数を直接呼ぶ
// テストではその欠陥をまったく検出できない。

// typeBase は type URI の名前空間。テンプレート利用者が problem.go の problemTypeBase を
// 書き換えれば、この期待値だけを直せばよい（差し替え箇所が 1 つであることをテストが示す）。
const typeBase = "https://github.com/example/go-ddd-template/problems/"

// ogenLeaks は応答本文に絶対に現れてはならない文字列（規則 R-11 / NFR-1）。
var ogenLeaks = []string{
	"operation ", "decode request", "decode params", "callback:", "unexpected byte",
	"field required", "openapi", "ogen", ".go:",
}

// errOpaque は classify のどの分岐にも当たらないエラー（500 経路の再現用）。
var errOpaque = errors.New("想定外の失敗")

// problemBody は problem+json の応答本文。生成型ではなくワイヤ形式そのものを見る。
type problemBody struct {
	Type          string         `json:"type"`
	Title         string         `json:"title"`
	Status        int            `json:"status"`
	Detail        string         `json:"detail"`
	InvalidParams []invalidParam `json:"invalid-params"`
	// raw は生の本文（ogen 由来文字列の非露出を確認するために保持する）。
	raw string
}

type invalidParam struct {
	Name   string `json:"name"`
	Code   string `json:"code"`
	Reason string `json:"reason"`
}

// readProblem は応答を problem+json として読み、全経路に共通の不変条件を検証して返す。
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

	assert.Equal(t, wantStatus, pb.Status, "problem.status は HTTP ステータスと一致する")
	assert.Equal(t, typeBase+wantTypeSuffix, pb.Type, "problem.type")
	assert.NotEqual(t, "about:blank", pb.Type, "type は about:blank ではない（FR-5.1）")
	assert.NotEmpty(t, pb.Title, "problem.title")
	assert.NotEmpty(t, pb.Detail, "problem.detail")

	for _, leak := range ogenLeaks {
		assert.NotContains(t, pb.raw, leak, "ogen / Go 由来の文言が漏れている")
	}
	return pb
}

// send は任意のメソッド・Content-Type でリクエストを送る。
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

// postJSONBody は JSON ボディを POST する短縮形。
func postJSONBody(t *testing.T, ts *httptest.Server, path, body string) *http.Response {
	t.Helper()
	return send(t, ts, http.MethodPost, path, "application/json", body)
}

// orderLine / orderBody はテスト入力を組み立てる（1 箇所だけ壊して振る舞いを見るため）。
func orderLine(sku string, qty int, amount int64, currency string) string {
	return fmt.Sprintf(`{"sku":%q,"quantity":%d,"unitPrice":{"amount":%d,"currency":%q}}`, sku, qty, amount, currency)
}

func orderBody(customer string, lines ...string) string {
	return fmt.Sprintf(`{"customerId":%q,"lines":[%s]}`, customer, strings.Join(lines, ","))
}

// ---------------------------------------------------------------------------
// E1: 契約検証（400 / 415）
// ---------------------------------------------------------------------------

func TestProblem_E1_ContractValidation(t *testing.T) {
	ts := newServer(t, stubReserver{})

	t.Run("境界: 必須欠落は兄弟フィールドを全件列挙する", func(t *testing.T) {
		pb := readProblem(t, postJSONBody(t, ts, "/orders", `{}`),
			http.StatusBadRequest, problem.TypeValidationError)

		names := make([]string, 0, len(pb.InvalidParams))
		for _, p := range pb.InvalidParams {
			names = append(names, p.Name)
			assert.Equal(t, problem.CodeRequired, p.Code)
			assert.Equal(t, problem.ReasonOf(problem.CodeRequired), p.Reason)
		}
		assert.ElementsMatch(t, []string{"customerId", "lines"}, names)
	})

	t.Run("異常系: 型不一致はラップ列からパスを組み添字を付けない（規則 R-9）", func(t *testing.T) {
		pb := readProblem(t, postJSONBody(t, ts, "/orders",
			orderBody("CUST-1", `{"sku":"SKU-A","quantity":"three","unitPrice":{"amount":1,"currency":"JPY"}}`)),
			http.StatusBadRequest, problem.TypeValidationError)

		require.Len(t, pb.InvalidParams, 1)
		assert.Equal(t, "lines.quantity", pb.InvalidParams[0].Name)
		assert.Equal(t, problem.CodeType, pb.InvalidParams[0].Code)
	})

	t.Run("異常系: 不正 JSON は特定できないので invalid-params をキーごと省略する（規則 R-14）", func(t *testing.T) {
		pb := readProblem(t, postJSONBody(t, ts, "/orders", `{"customerId":`),
			http.StatusBadRequest, problem.TypeValidationError)

		assert.Empty(t, pb.InvalidParams)
		assert.NotContains(t, pb.raw, "invalid-params", "空配列ではなくキーごと省略する")
	})

	t.Run("境界: 空ボディは body を指す", func(t *testing.T) {
		pb := readProblem(t, postJSONBody(t, ts, "/orders", ``),
			http.StatusBadRequest, problem.TypeValidationError)
		require.Len(t, pb.InvalidParams, 1)
		assert.Equal(t, problem.CodeBodyRequired, pb.InvalidParams[0].Code)
	})

	t.Run("異常系: Content-Type 不正は 415", func(t *testing.T) {
		pb := readProblem(t, send(t, ts, http.MethodPost, "/orders", "text/plain", placeBody),
			http.StatusUnsupportedMediaType, problem.TypeUnsupportedMediaType)
		assert.Empty(t, pb.InvalidParams)
	})
}

// ---------------------------------------------------------------------------
// E2 / E3: ルーティング不一致（404）とメソッド不許可（405）
// ---------------------------------------------------------------------------

func TestProblem_E2_NotFoundIsProblemJSON(t *testing.T) {
	ts := newServer(t, stubReserver{})

	pb := readProblem(t, send(t, ts, http.MethodGet, "/no-such-endpoint", "", ""),
		http.StatusNotFound, problem.TypeNotFound)

	assert.NotContains(t, pb.raw, "404 page not found", "Go 標準のプレーンテキストが出ない")
	assert.Empty(t, pb.InvalidParams, "経路が無いだけなのでフィールドに帰着しない")
}

func TestProblem_E3_MethodNotAllowed(t *testing.T) {
	ts := newServer(t, stubReserver{})

	t.Run("異常系: 405 は problem+json で Allow ヘッダを維持する", func(t *testing.T) {
		resp := send(t, ts, http.MethodDelete, "/orders", "", "")
		allow := resp.Header.Get("Allow")
		pb := readProblem(t, resp, http.StatusMethodNotAllowed, problem.TypeMethodNotAllowed)

		assert.Contains(t, allow, http.MethodPost, "Allow は本文書き出し前に設定される")
		assert.Empty(t, pb.InvalidParams)
	})

	t.Run("正常系: OPTIONS は 405 にせず CORS プリフライトを壊さない", func(t *testing.T) {
		resp := send(t, ts, http.MethodOptions, "/orders", "", "")
		require.NoError(t, resp.Body.Close())
		assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	})
}

// ---------------------------------------------------------------------------
// E4: ドメイン検証（422）と、既存経路の type 移行
// ---------------------------------------------------------------------------

func TestProblem_E4_DomainValidationHasInvalidParams(t *testing.T) {
	ts := newServer(t, stubReserver{})

	cases := []struct {
		name     string
		body     string
		wantName string
		wantCode string
	}{
		{"数量 0", orderBody("CUST-1", orderLine("SKU-A", 0, 100, "JPY")), "lines[0].quantity", order.VQuantity.Code},
		{"SKU 空", orderBody("CUST-1", orderLine(" ", 1, 100, "JPY")), "lines[0].sku", order.VSKU.Code},
		{"金額が負", orderBody("CUST-1", orderLine("SKU-A", 1, -1, "JPY")), "lines[0].unitPrice.amount", order.VMoneyAmount.Code},
		{"通貨が空", orderBody("CUST-1", orderLine("SKU-A", 1, 100, "")), "lines[0].unitPrice.currency", order.VMoneyCurrency.Code},
		{"顧客 ID 空", orderBody("  ", orderLine("SKU-A", 1, 100, "JPY")), "customerId", order.VCustomerID.Code},
		{"明細が空（集約規則）", orderBody("CUST-1"), "lines", order.VEmptyOrder.Code},
		{
			"2 行目が壊れている（添字が正しい行を指す）",
			orderBody("CUST-1", orderLine("SKU-A", 1, 100, "JPY"), orderLine("SKU-B", 0, 100, "JPY")),
			"lines[1].quantity", order.VQuantity.Code,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pb := readProblem(t, postJSONBody(t, ts, "/orders", tc.body),
				http.StatusUnprocessableEntity, problem.TypeInvalidInput)

			require.Len(t, pb.InvalidParams, 1)
			assert.Equal(t, tc.wantName, pb.InvalidParams[0].Name, "JSON のフィールドパス（規則 R-8 / R-10）")
			assert.Equal(t, tc.wantCode, pb.InvalidParams[0].Code)
			assert.NotEmpty(t, pb.InvalidParams[0].Reason)
		})
	}
}

// パスパラメータに対する 422。Go の識別子（OrderID）ではなく HTTP の名前（id）が出る。
func TestProblem_E4_PathParameterUsesHTTPName(t *testing.T) {
	ts := newServer(t, stubReserver{})

	pb := readProblem(t, send(t, ts, http.MethodGet, "/orders/%20%20", "", ""),
		http.StatusUnprocessableEntity, problem.TypeInvalidInput)

	require.Len(t, pb.InvalidParams, 1)
	assert.Equal(t, "id", pb.InvalidParams[0].Name, "Go 識別子 OrderID を露出しない（規則 R-10）")
	assert.Equal(t, order.VOrderID.Code, pb.InvalidParams[0].Code)
}

// FR-4.3 の中核。番兵が同じ ErrInvalidMoney でも、応答レベルで amount と currency が
// 区別されることを 1 つのテストで並べて固定する。
func TestProblem_E4_MoneyAmountAndCurrencyAreDistinguished(t *testing.T) {
	ts := newServer(t, stubReserver{})

	amount := readProblem(t,
		postJSONBody(t, ts, "/orders", orderBody("CUST-1", orderLine("SKU-A", 1, -1, "JPY"))),
		http.StatusUnprocessableEntity, problem.TypeInvalidInput)
	currency := readProblem(t,
		postJSONBody(t, ts, "/orders", orderBody("CUST-1", orderLine("SKU-A", 1, 1, ""))),
		http.StatusUnprocessableEntity, problem.TypeInvalidInput)

	require.Len(t, amount.InvalidParams, 1)
	require.Len(t, currency.InvalidParams, 1)
	assert.NotEqual(t, amount.InvalidParams[0].Name, currency.InvalidParams[0].Name)
	assert.NotEqual(t, amount.InvalidParams[0].Code, currency.InvalidParams[0].Code)
	assert.NotEqual(t, amount.InvalidParams[0].Reason, currency.InvalidParams[0].Reason)
}

// NFR-4.2: 既存の NewError 経路の type 移行が実際に効いたことを固定する。
// この系統が無いと FR-5.2 の retrofit が効いた保証を誰も持たない。
func TestProblem_E4_TypeMigration(t *testing.T) {
	cases := []struct {
		name       string
		reserver   stubReserver
		method     string
		path       string
		body       string
		wantStatus int
		wantSuffix string
	}{
		{
			name:     "契約: 404 は resource-not-found で E2 の not-found とは別種別",
			reserver: stubReserver{}, method: http.MethodGet, path: "/orders/NOPE",
			wantStatus: http.StatusNotFound, wantSuffix: problem.TypeResourceNotFound,
		},
		{
			name:     "契約: 在庫予約の拒否の 409 は reservation-rejected",
			reserver: stubReserver{err: application.ErrReservationRejected},
			method:   http.MethodPost, path: "/orders", body: placeBody,
			wantStatus: http.StatusConflict, wantSuffix: problem.TypeReservationRejected,
		},
		{
			name:     "契約: 422 は invalid-input",
			reserver: stubReserver{}, method: http.MethodPost, path: "/orders",
			body:       `{"customerId":"CUST-1","lines":[]}`,
			wantStatus: http.StatusUnprocessableEntity, wantSuffix: problem.TypeInvalidInput,
		},
		{
			name: "契約: 503 は service-unavailable",
			reserver: stubReserver{
				err: errors.Join(application.ErrReservationRejected, application.ErrReservationUnavailable),
			},
			method: http.MethodPost, path: "/orders", body: placeBody,
			wantStatus: http.StatusServiceUnavailable, wantSuffix: problem.TypeServiceUnavailable,
		},
		{
			name:     "契約: 500 は internal-error",
			reserver: stubReserver{err: errOpaque},
			method:   http.MethodPost, path: "/orders", body: placeBody,
			wantStatus: http.StatusInternalServerError, wantSuffix: problem.TypeInternalError,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ts := newServer(t, tc.reserver)
			contentType := ""
			if tc.body != "" {
				contentType = "application/json"
			}
			pb := readProblem(t, send(t, ts, tc.method, tc.path, contentType, tc.body), tc.wantStatus, tc.wantSuffix)
			if tc.wantStatus != http.StatusUnprocessableEntity {
				assert.Empty(t, pb.InvalidParams, "422 以外は入力フィールドに帰着しない（規則 R-14）")
			}
		})
	}
}

// 500 の detail に元のエラー文言が出ないこと（規則 R-11）。
func TestProblem_E4_InternalErrorHidesCause(t *testing.T) {
	ts := newServer(t, stubReserver{err: errOpaque})

	pb := readProblem(t, postJSONBody(t, ts, "/orders", placeBody),
		http.StatusInternalServerError, problem.TypeInternalError)

	assert.NotContains(t, pb.raw, errOpaque.Error(), "原因の文言を外へ出さない")
	assert.Equal(t, problem.DetailInternalError, pb.Detail)
}

// 409 は「現在状態との矛盾」と「在庫予約の拒否」で別の type を持つ（規則 R-2）。
// 同じ status で違う type が出ることこそ type URI を導入した価値そのものである。
func TestProblem_E4_SameStatusDifferentType(t *testing.T) {
	rejectedTS := newServer(t, stubReserver{err: application.ErrReservationRejected})
	rejected := readProblem(t, postJSONBody(t, rejectedTS, "/orders", placeBody),
		http.StatusConflict, problem.TypeReservationRejected)

	okTS := newServer(t, stubReserver{})
	created := postJSONBody(t, okTS, "/orders", placeBody)
	var body struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.NewDecoder(created.Body).Decode(&body))
	require.NoError(t, created.Body.Close())

	// 1 回目の取消は成功し、2 回目は「確定状態ではない」で 409 になる。
	require.NoError(t, postJSONBody(t, okTS, "/orders/"+body.ID+"/cancel", "").Body.Close())
	conflict := readProblem(t, postJSONBody(t, okTS, "/orders/"+body.ID+"/cancel", ""),
		http.StatusConflict, problem.TypeConflict)

	assert.Equal(t, rejected.Status, conflict.Status, "status は同じ")
	assert.NotEqual(t, rejected.Type, conflict.Type, "type は異なる（クライアントはこれで分岐できる）")
	assert.NotEqual(t, rejected.Title, conflict.Title, "title も type と 1 対 1（規則 R-3）")
}

// TestProblem_InvalidParamCodeEnumCoversVocabulary は、このサーバが応答に載せうる code
// 語彙のすべてが、契約（openapi.yaml）の InvalidParam.code enum に含まれることを網羅的に
// 検証する。列挙元は語彙の唯一の情報源そのもの——契約検証語彙（shared/problem の Code*）と、
// 注文コンテキストが所有するドメイン検証語彙（order.Rule の Code）——である。
//
// readProblem 内の Validate はテストが実際に踏んだ経路の code しか検証できない（例えば
// invalid_reservation_ref は公開 API から容易には誘発できない）。この網羅テストは経路に
// 依存せず、語彙 → enum の対応を直接固定する。新しい order.Rule を足したのに契約の enum へ
// 足し忘れれば、その code の生成型 Validate が invalid value を返し CI が落ちる（規則 R-19）。
func TestProblem_InvalidParamCodeEnumCoversVocabulary(t *testing.T) {
	// 契約検証語彙（400 / validation-error）。shared/problem/vocab.go の Code* が唯一の情報源。
	contractCodes := []string{
		problem.CodeRequired, problem.CodeType, problem.CodeMinLength, problem.CodeMaxLength,
		problem.CodePattern, problem.CodeUniqueItems, problem.CodeInvalidParam,
		problem.CodeBodyRequired, problem.CodeInvalid,
	}
	// ドメイン検証語彙（422 / invalid-input）。注文コンテキストの order.Rule が唯一の情報源。
	domainCodes := []string{
		order.VEmptyOrder.Code, order.VSKU.Code, order.VQuantity.Code,
		order.VMoneyAmount.Code, order.VMoneyCurrency.Code, order.VCustomerID.Code,
		order.VOrderID.Code, order.VReservationRef.Code,
	}

	for _, code := range append(contractCodes, domainCodes...) {
		assert.NoErrorf(t, openapi.InvalidParamCode(code).Validate(),
			"code %q は契約（openapi.yaml）の InvalidParam.code enum に含まれる必要がある", code)
	}
}
