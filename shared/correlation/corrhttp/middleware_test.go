package corrhttp_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/example/go-ddd-template/shared/correlation"
	"github.com/example/go-ddd-template/shared/correlation/corrhttp"
)

// captureHandler は context に載った相関 ID を捕捉する終端ハンドラ。
func captureHandler(captured *string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*captured = correlation.FromContextOrEmpty(r.Context())
		w.WriteHeader(http.StatusOK)
	})
}

// TestMiddleware_InheritsTraceparent は、受信 traceparent の trace-id が相関 ID として
// 引き継がれ、レスポンスヘッダにも反映されることを確認する（経路 a）。
func TestMiddleware_InheritsTraceparent(t *testing.T) {
	const traceID = "0af7651916cd43dd8448eb211c80319c"
	var got string
	h := corrhttp.Middleware(captureHandler(&got))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("traceparent", "00-"+traceID+"-b7ad6b7169203331-01")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, traceID, got, "traceparent の trace-id を相関 ID に引き継ぐ")
	assert.Equal(t, traceID, rec.Header().Get("X-Correlation-ID"), "レスポンスに X-Correlation-ID")
	assert.Contains(t, rec.Header().Get("traceparent"), traceID, "レスポンスに traceparent")
}

// TestMiddleware_UsesCorrelationIDHeader は、traceparent が無く X-Correlation-ID のみの
// 場合にそれを引き継ぐことを確認する（経路 b）。
func TestMiddleware_UsesCorrelationIDHeader(t *testing.T) {
	var got string
	h := corrhttp.Middleware(captureHandler(&got))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Correlation-ID", "corr-xyz")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, "corr-xyz", got, "X-Correlation-ID を相関 ID に引き継ぐ")
	assert.Equal(t, "corr-xyz", rec.Header().Get("X-Correlation-ID"), "レスポンスに反映")
	assert.Empty(t, rec.Header().Get("traceparent"), "32 桁 16 進でなければ traceparent は付かない")
}

// TestMiddleware_GeneratesNewIDWhenAbsent は、どちらのヘッダも無い場合に新規採番し、
// 採番した ID（32 桁 16 進）を X-Correlation-ID と traceparent の両方で返すことを確認する（経路 c / d）。
func TestMiddleware_GeneratesNewIDWhenAbsent(t *testing.T) {
	var got string
	h := corrhttp.Middleware(captureHandler(&got))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	require.NotEmpty(t, got, "どちらも無ければ新規採番する")
	assert.Equal(t, got, rec.Header().Get("X-Correlation-ID"), "採番した ID をレスポンスに反映")
	assert.Contains(t, rec.Header().Get("traceparent"), got, "新規採番 ID は 32 桁 16 進なので traceparent も載る")
}

// TestMiddleware_TraceparentTakesPriority は、traceparent と X-Correlation-ID の両方が
// あるとき traceparent が優先されることを確認する（R-10 の取り込み優先順位）。
func TestMiddleware_TraceparentTakesPriority(t *testing.T) {
	const traceID = "0af7651916cd43dd8448eb211c80319c"
	var got string
	h := corrhttp.Middleware(captureHandler(&got))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("traceparent", "00-"+traceID+"-b7ad6b7169203331-01")
	req.Header.Set("X-Correlation-ID", "corr-loser")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, traceID, got, "traceparent が X-Correlation-ID より優先される")
}
