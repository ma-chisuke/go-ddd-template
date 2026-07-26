// 注文コンテキストの公開 HTTP 受信アダプタの fuzz ターゲット（F-1 / R-27〜R-29）。
//
// 性質テスト（rapid）とは受け持つ主張が違う。rapid はドメインの**代数的法則と操作列の
// 不変条件**を、fuzz は**境界の全域性**——任意の入力で壊れないこと——を担う。
// 単位は上流の判定基準どおり「受信サーバ 1 つにつき 1 ターゲット」である。
//
// このターゲットが主張するのは 3 点。
//
//	R-27 いかなる入力でもハンドラが panic しない
//	R-28 エラー応答に内部実装由来の文言が現れない
//	R-29 エラー応答の Content-Type は必ず application/problem+json
//
// seed corpus は 2 つの経路で入る。f.Add に書いた種（`seed#N` として実行される）と、
// testdata/fuzz/FuzzHTTP/ にコミットした種（ファイル名で実行される）である。どちらも
// 通常の `go test` で毎回実行されるので、長時間の -fuzz をマージゲートに載せなくても
// 回帰テストとして効く。長時間の探索は `make fuzz` で任意に行う。

package httpapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// fuzzMethods は 1 つの入力に対して試す HTTP メソッド。
//
// メソッドを corpus の項目にせず固定リストの走査にしているのは、同じ path と body の組を
// 3 つのメソッドで必ず試すためである（corpus の項目にすると探索まかせになり、
// 405 の経路を一度も踏まない corpus がありうる）。
var fuzzMethods = []string{http.MethodGet, http.MethodPost, http.MethodPut}

// internalMarkers はエラー応答に現れてはならない内部実装由来の文言（R-28）。
//
// 先頭の "decode request" は ogen の**既定**エラーハンドラが返す文言である
// （`{"error_message": "operation placeOrder: decode request: ..."}`）。これが本文に出たら
// Handler.ServerOptions の注入が外れており、内部実装が外へ漏れている。
var internalMarkers = []string{
	"decode request",
	"ogen",
	"pgx",
	"database/sql",
	"sql:",
	"goroutine ",
	"runtime error",
	"panic:",
	".go:",
	"github.com/example/go-ddd-template/contexts",
	"internal/",
	"httpapi.",
	"application.",
	"domain.",
}

// FuzzHTTP は注文コンテキストの公開 HTTP のデコード境界を探索する。
func FuzzHTTP(f *testing.F) {
	// 種は既存の httpapi_test.go のケースから起こす。実際にハンドラを通る形のリクエストは、
	// fuzz エンジンが構造を壊しながら探索する出発点として最も情報量が多い。
	f.Add("/orders", placeBody)
	f.Add("/orders", `{"customerId":"CUST-1","lines":[]}`)
	f.Add("/orders", `{"customerId":1,"lines":"文字列ではない"}`)
	f.Add("/orders", `{`)
	f.Add("/orders", "")
	f.Add("/orders?trace=1", placeBody)
	f.Add("/orders/NOPE", "")
	f.Add("/orders/NOPE/cancel", "")
	f.Add("/", "")
	f.Add("/unknown", "")

	f.Fuzz(func(t *testing.T, path, body string) {
		// ハンドラは入力ごとに組み立てる。状態を持ち越すと、ある入力の失敗が直前の入力に
		// 依存し、再現に corpus 全体の順序が要るようになる。
		handler := newHandler(t, stubReserver{})

		for _, method := range fuzzMethods {
			req, err := http.NewRequestWithContext(
				context.Background(), method, "http://ordering.test"+path, strings.NewReader(body))
			if err != nil {
				// URL として組み立てられない入力は HTTP サーバまで到達しないので、
				// このターゲットの守備範囲の外である。
				continue
			}
			req.RequestURI = req.URL.RequestURI()
			req.Header.Set("Content-Type", "application/json")

			rec := httptest.NewRecorder()
			// R-27: panic しない。httptest サーバを経由せず直接 ServeHTTP を呼ぶので、
			// ハンドラが panic すればこのゴルーチンごと落ちてテストの失敗になる
			//（サーバ越しだと net/http が panic を回復してしまい、失敗として現れない）。
			handler.ServeHTTP(rec, req)

			resp := rec.Result()
			payload := rec.Body.String()
			resp.Body.Close()

			if resp.StatusCode < http.StatusBadRequest {
				// 成功応答は入力を反響する（作成した注文の customerId など）ので、
				// 内部文言の検査対象にしない。R-28 が守るのは、定型文だけを返すはずの
				// エラー経路である。
				continue
			}
			// R-29。
			assert.Equal(t, "application/problem+json", resp.Header.Get("Content-Type"),
				"エラー応答は必ず problem+json で返る")
			// R-28。
			for _, marker := range internalMarkers {
				assert.NotContains(t, payload, marker, "エラー応答に内部実装の文言が漏れている")
			}
		}
	})
}
