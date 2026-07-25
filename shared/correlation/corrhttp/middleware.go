// Package corrhttp は相関 ID を HTTP 境界で確立するミドルウェアを提供する。
//
// net/http への依存はこのサブパッケージに閉じ、ポートパッケージ shared/correlation の
// 本体は純粋な文字列処理（traceparent コーデック + context キー）のまま保つ。
// 現存する 3 つのサーバ（注文の公開・在庫の公開・在庫の内部）は、いずれもこの同一の
// ミドルウェアを使うことで相関 ID の取り込み規則を一箇所に集約する。
package corrhttp

import (
	"net/http"

	"github.com/example/go-ddd-template/shared/correlation"
	"github.com/example/go-ddd-template/shared/id"
)

const (
	// headerCorrelationID はリクエスト／レスポンスで相関 ID を運ぶ HTTP ヘッダ名。
	headerCorrelationID = "X-Correlation-ID"
	// headerTraceparent は W3C Trace Context のヘッダ名。
	headerTraceparent = "traceparent"
)

// Middleware は相関 ID を確立する HTTP ミドルウェア。
//
// 受信リクエストの W3C traceparent ヘッダがあればその trace-id を相関 ID として引き継ぎ、
// 無ければ X-Correlation-ID、それも無ければ新規採番して context に載せる。相関 ID は
// レスポンスヘッダにも反映し、32 桁 16 進のときは traceparent としても返す。
//
// これにより place -> reserve -> confirm / cancel -> release のフローが、注文サービスの
// 入口から在庫サービスのログまで、共有 trace_id でサービスを跨いで相関する。
//
// 重要: context に載せてよいのはこうしたリクエストスコープの付帯情報だけであり、
// トランザクションハンドルのような制御関心を context に隠して運んではならない。
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cid := correlationIDFromRequest(r)
		if cid == "" {
			cid = id.New()
		}
		ctx := correlation.WithID(r.Context(), cid)
		w.Header().Set(headerCorrelationID, cid)
		if tp, ok := correlation.Traceparent(cid); ok {
			w.Header().Set(headerTraceparent, tp)
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// correlationIDFromRequest は受信リクエストから相関 ID を取り出す。traceparent を優先し、
// 無ければ X-Correlation-ID を用いる（どちらも無ければ空文字）。
func correlationIDFromRequest(r *http.Request) string {
	if tp := r.Header.Get(headerTraceparent); tp != "" {
		if traceID, ok := correlation.TraceIDFromTraceparent(tp); ok {
			return traceID
		}
	}
	return r.Header.Get(headerCorrelationID)
}
