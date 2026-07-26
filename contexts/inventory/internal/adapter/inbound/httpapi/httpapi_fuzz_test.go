// 在庫コンテキストの公開 HTTP 受信アダプタの fuzz ターゲット（F-2 / R-27〜R-29）。
//
// 主張は注文側の FuzzHTTP と同じ 3 点である。
//
//	R-27 いかなる入力でもハンドラが panic しない
//	R-28 エラー応答に内部実装由来の文言が現れない
//	R-29 エラー応答の Content-Type は必ず application/problem+json
//
// 単位は上流の判定基準どおり「受信サーバ 1 つにつき 1 ターゲット」なので、注文側と
// 共有せずコンテキストごとに書く。応答の語彙も種別 URI の名前空間もコンテキストが所有する
// （problem.go の reason 表は共有していない）ため、ここを 1 本にまとめると
// 「どちらのコンテキストの契約を検証しているのか」が曖昧になる。

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
// 先頭の "decode request" は ogen の**既定**エラーハンドラが返す文言である。これが本文に
// 出たら Handler.ServerOptions の注入が外れており、内部実装が外へ漏れている。
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

// FuzzHTTP は在庫コンテキストの公開 HTTP のデコード境界を探索する。
func FuzzHTTP(f *testing.F) {
	// 種は既存の httpapi_test.go のケースから起こす。
	f.Add("/stock/WIDGET-001/replenish", `{"quantity":10}`)
	f.Add("/stock/WIDGET-001/replenish", `{"quantity":0}`)
	f.Add("/stock/WIDGET-001/replenish", `{"quantity":-1}`)
	f.Add("/stock/WIDGET-001/replenish", `{"quantity":"文字列ではない"}`)
	f.Add("/stock/WIDGET-001/replenish", `{`)
	f.Add("/stock/WIDGET-001/replenish", "")
	f.Add("/stock/WIDGET-001", "")
	f.Add("/stock/MISSING", "")
	f.Add("/stock//replenish", `{"quantity":1}`)
	f.Add("/", "")
	f.Add("/unknown", "")

	f.Fuzz(func(t *testing.T, path, body string) {
		// ハンドラは入力ごとに組み立てる。状態を持ち越すと、ある入力の失敗が直前の入力に
		// 依存し、再現に corpus 全体の順序が要るようになる。
		handler := newHandler(t)

		for _, method := range fuzzMethods {
			req, err := http.NewRequestWithContext(
				context.Background(), method, "http://inventory.test"+path, strings.NewReader(body))
			if err != nil {
				// URL として組み立てられない入力は HTTP サーバまで到達しない。
				continue
			}
			req.RequestURI = req.URL.RequestURI()
			req.Header.Set("Content-Type", "application/json")

			rec := httptest.NewRecorder()
			// R-27: panic しない。httptest サーバを経由せず直接 ServeHTTP を呼ぶので、
			// ハンドラが panic すればこのゴルーチンごと落ちてテストの失敗になる。
			handler.ServeHTTP(rec, req)

			resp := rec.Result()
			payload := rec.Body.String()
			resp.Body.Close()

			if resp.StatusCode < http.StatusBadRequest {
				// 成功応答は入力（SKU など）を反響するので内部文言の検査対象にしない。
				// R-28 が守るのは、定型文だけを返すはずのエラー経路である。
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
