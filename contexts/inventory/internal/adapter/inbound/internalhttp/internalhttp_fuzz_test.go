// 在庫コンテキストの内部 HTTP 受信アダプタの fuzz ターゲット（F-3 / R-27〜R-30）。
//
// 公開 HTTP の 2 ターゲットと同じ 3 点に加え、この境界だけが持つ主張が 1 つある。
//
//	R-27 いかなる入力でもハンドラが panic しない
//	R-28 エラー応答に内部実装由来の文言が現れない
//	R-29 エラー応答の Content-Type は必ず application/problem+json
//	R-30 /events の取り込みで**未知のイベント種別は 422** になる
//
// R-30 は「任意のボディで 422」ではない。契約に適合しないボディは種別を解決する前に
// 400 で弾かれるので、任意入力に対して 422 を主張すると偽の失敗を報告する。
// 主張が成立するのは「契約としては妥当だが種別が未登録」な封筒に限られるため、
// その条件を wantsUnknownTypeRejection で明示的に切り出している。

package internalhttp_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/example/go-ddd-template/contexts/inventory/internal/application"
)

// fuzzMethods は 1 つの入力に対して試す HTTP メソッド。
var fuzzMethods = []string{http.MethodGet, http.MethodPost, http.MethodPut}

// internalMarkers はエラー応答に現れてはならない内部実装由来の文言（R-28）。
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
	"internalhttp.",
	"application.",
	"domain.",
}

// knownMessageTypes は Router に登録済みの種別。ここに無い種別が R-30 の「未知」である。
// newInternalHandler の router.Register と同じ 2 つを指しており、片方だけ増えれば
// R-30 の判定がずれるので、登録の追加時はここも直す。
var knownMessageTypes = map[string]bool{
	application.MessageTypeConfirmReservation: true,
	application.MessageTypeOrderCancelled:     true,
}

// wantsUnknownTypeRejection は「契約としては妥当だが種別が未登録」な /events の封筒かを返す。
//
// 契約（InboundMessage）は id / type / payload の 3 つを必須の文字列として要求する。
// 任意項目（trace_id / occurred_at）を持つ封筒を対象から外すのは、occurred_at に
// date-time 形式の検証があり、不正なら種別解決に至る前に 400 で落ちるためである。
// 「400 で落ちたのか 422 で落ちたのか」を区別できない条件で 422 を主張しても主張にならない。
func wantsUnknownTypeRejection(body string) bool {
	var envelope map[string]string
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		return false
	}
	if len(envelope) != 3 {
		return false
	}
	for _, key := range []string{"id", "type", "payload"} {
		if _, ok := envelope[key]; !ok {
			return false
		}
	}
	return !knownMessageTypes[envelope["type"]]
}

// FuzzInternalHTTP は在庫コンテキストの内部 HTTP のデコード境界を探索する。
func FuzzInternalHTTP(f *testing.F) {
	// 種は既存の internalhttp_test.go のケースから起こす。
	f.Add("/reservations", `{"ref":"ORDER-1","lines":[{"sku":"WIDGET-001","quantity":4}]}`)
	f.Add("/reservations", `{"ref":"ORDER-1","lines":[{"sku":"WIDGET-001","quantity":999}]}`)
	f.Add("/reservations", `{"ref":"","lines":[]}`)
	f.Add("/reservations", `{`)
	f.Add("/reservations", "")
	f.Add("/reservations/ORDER-1/confirm", "")
	f.Add("/reservations/NEVER/confirm", "")
	f.Add("/reservations/ORDER-1/release", "")
	f.Add("/events", `{"id":"m-1","type":"ordering.order.cancelled","payload":"{\"reservation_ref\":\"ORDER-1\"}"}`)
	f.Add("/events", `{"id":"m-1","type":"unknown.type","payload":"{}"}`)
	f.Add("/events", `{"id":"m-1","type":"","payload":"{}"}`)
	f.Add("/events", `{"id":1,"type":"unknown.type","payload":"{}"}`)
	f.Add("/", "")
	f.Add("/unknown", "")

	f.Fuzz(func(t *testing.T, path, body string) {
		// ハンドラは入力ごとに組み立てる。内部 HTTP は予約という状態を持つので、
		// 持ち越すと失敗の再現に corpus 全体の順序が要るようになる。
		handler := newInternalHandler(t)

		for _, method := range fuzzMethods {
			req, err := http.NewRequestWithContext(
				context.Background(), method, "http://inventory-internal.test"+path, strings.NewReader(body))
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

			// R-30: 契約に適合した封筒で種別が未登録なら 422。
			if method == http.MethodPost && req.URL.Path == "/events" && wantsUnknownTypeRejection(body) {
				assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode,
					"未知のイベント種別は 422 で拒否される")
			}

			if resp.StatusCode < http.StatusBadRequest {
				// 成功応答は入力（予約参照など）を反響するので内部文言の検査対象にしない。
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
