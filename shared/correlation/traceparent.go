package correlation

import (
	"strings"

	"github.com/example/go-ddd-template/shared/id"
)

// zeroTraceID は W3C Trace Context で無効とされる全ゼロの trace-id。
const zeroTraceID = "00000000000000000000000000000000"

// Traceparent は 32 桁 16 進の相関 ID を trace-id とみなし、W3C traceparent
// （version-traceid-spanid-flags）を組み立てる。
//
// 相関 ID が 32 桁 16 進でない場合は traceparent を組み立てず ok=false を返す
// （呼び出し側は X-Correlation-ID のみを送る／載せる）。span-id はホップごとに
// 新規採番する（16 桁 16 進）ため、同じ trace-id でもホップごとに異なる span を持つ。
//
// 純粋な文字列処理であり net/http には依存しない。HTTP ヘッダとしての取り回しは
// correlation/corrhttp（受信側）と各コンテキストの送信アダプタ（送信側）が担う。
func Traceparent(traceID string) (string, bool) {
	if len(traceID) != 32 || !IsHex(traceID) {
		return "", false
	}
	spanID := id.New()[:16]
	return "00-" + traceID + "-" + spanID + "-01", true
}

// TraceIDFromTraceparent は W3C traceparent（version-traceid-spanid-flags）から
// trace-id（32 桁 16 進）を取り出す。区切りが 4 分割でない、trace-id が 32 桁でない、
// 全ゼロ、または非 16 進のいずれかであれば ok=false を返す。
func TraceIDFromTraceparent(tp string) (string, bool) {
	parts := strings.Split(tp, "-")
	if len(parts) != 4 {
		return "", false
	}
	traceID := parts[1]
	if len(traceID) != 32 || traceID == zeroTraceID || !IsHex(traceID) {
		return "", false
	}
	return traceID, true
}

// IsHex は文字列が 16 進数字のみで構成されるかを返す（空文字列は真）。
func IsHex(s string) bool {
	for _, c := range s {
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f', c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
}
