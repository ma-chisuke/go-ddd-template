package aclhttp

import (
	"net/http"
	"time"

	invclient "github.com/example/go-ddd-template/clients/inventory/invclient"
	"github.com/example/go-ddd-template/shared/correlation"
	"github.com/example/go-ddd-template/shared/id"
)

const (
	// headerCorrelationID はサービス間で相関 ID を運ぶヘッダ。在庫側のミドルウェアが読む。
	headerCorrelationID = "X-Correlation-ID"
	// headerTraceparent は W3C Trace Context のヘッダ。分散トレースの相関に使う。
	headerTraceparent = "traceparent"
)

// correlationTransport は context の相関 ID を、送出するリクエストのヘッダへ載せる
// RoundTripper。これにより place -> reserve / cancel -> release のフローが在庫サービスの
// ログまでサービスを跨いで相関する（NFR: seam を跨ぐ相関）。
type correlationTransport struct {
	base http.RoundTripper
}

// RoundTrip は相関 ID をヘッダへ付与してから下位トランスポートへ委譲する。
func (t *correlationTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	cid := correlation.FromContextOrEmpty(req.Context())
	if cid == "" {
		return base.RoundTrip(req)
	}
	// RoundTripper は渡されたリクエストを変更してはならない規約なので複製する。
	clone := req.Clone(req.Context())
	clone.Header.Set(headerCorrelationID, cid)
	if tp, ok := traceparent(cid); ok {
		clone.Header.Set(headerTraceparent, tp)
	}
	return base.RoundTrip(clone)
}

// traceparent は 32 桁 16 進の相関 ID を trace-id とみなし、W3C traceparent を組み立てる。
// 相関 ID が 32 桁 16 進でない場合は traceparent を組み立てない（X-Correlation-ID のみ送る）。
func traceparent(traceID string) (string, bool) {
	if len(traceID) != 32 || !isHex(traceID) {
		return "", false
	}
	// span-id はホップごとに新規採番する（16 桁 16 進）。
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

// NewInventoryClient は在庫の内部 API を呼ぶ生成クライアントを、相関 ID 伝播と全体
// タイムアウトを備えた HTTP クライアントとともに生成する。合成ルート（cmd）が、これを
// ACL アダプタ（Reserver）とイベント送信アダプタ（eventhttp）の双方へ注入する。
func NewInventoryClient(baseURL string, timeout time.Duration) (*invclient.Client, error) {
	httpClient := &http.Client{
		Timeout:   timeout,
		Transport: &correlationTransport{base: http.DefaultTransport},
	}
	return invclient.NewClient(baseURL, invclient.WithClient(httpClient))
}
