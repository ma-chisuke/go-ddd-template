package httpapi

import (
	"net/http"
	"strings"

	"github.com/example/go-ddd-template/shared/correlation"
	"github.com/example/go-ddd-template/shared/id"
)

const (
	// correlationHeader はリクエスト／レスポンスで相関 ID を運ぶ HTTP ヘッダ名。
	correlationHeader = "X-Correlation-ID"
	// traceparentHeader は W3C Trace Context のヘッダ名。
	traceparentHeader = "traceparent"
	// zeroTraceID は W3C で無効とされる全ゼロの trace-id。
	zeroTraceID = "00000000000000000000000000000000"
)

// CorrelationMiddleware は相関 ID を確立する HTTP ミドルウェア。
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
func CorrelationMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cid := correlationIDFromRequest(r)
		if cid == "" {
			cid = id.New()
		}
		ctx := correlation.WithID(r.Context(), cid)
		w.Header().Set(correlationHeader, cid)
		if tp, ok := traceparent(cid); ok {
			w.Header().Set(traceparentHeader, tp)
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// correlationIDFromRequest は受信リクエストから相関 ID を取り出す。traceparent を優先し、
// 無ければ X-Correlation-ID を用いる（どちらも無ければ空文字）。
func correlationIDFromRequest(r *http.Request) string {
	if tp := r.Header.Get(traceparentHeader); tp != "" {
		if traceID, ok := traceIDFromTraceparent(tp); ok {
			return traceID
		}
	}
	return r.Header.Get(correlationHeader)
}

// traceIDFromTraceparent は W3C traceparent（version-traceid-spanid-flags）から
// trace-id（32 桁 16 進）を取り出す。形式が不正、または全ゼロなら ok=false。
func traceIDFromTraceparent(tp string) (string, bool) {
	parts := strings.Split(tp, "-")
	if len(parts) != 4 {
		return "", false
	}
	traceID := parts[1]
	if len(traceID) != 32 || traceID == zeroTraceID || !isHex(traceID) {
		return "", false
	}
	return traceID, true
}

// traceparent は 32 桁 16 進の相関 ID を trace-id とする W3C traceparent を組み立てる。
func traceparent(traceID string) (string, bool) {
	if len(traceID) != 32 || !isHex(traceID) {
		return "", false
	}
	spanID := id.New()[:16]
	return "00-" + traceID + "-" + spanID + "-01", true
}

// isHex は文字列が 16 進数字のみで構成されるかを返す。
func isHex(s string) bool {
	for _, c := range s {
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f', c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
}
