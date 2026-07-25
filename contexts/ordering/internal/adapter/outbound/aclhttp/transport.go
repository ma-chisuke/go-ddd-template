package aclhttp

import (
	"net/http"
	"time"

	invclient "github.com/example/go-ddd-template/clients/inventory/invclient"
	"github.com/example/go-ddd-template/shared/correlation"
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
	if tp, ok := correlation.Traceparent(cid); ok {
		clone.Header.Set(headerTraceparent, tp)
	}
	return base.RoundTrip(clone)
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
