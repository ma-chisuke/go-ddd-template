package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	httpapi "github.com/example/go-ddd-template/contexts/inventory/internal/adapter/inbound/http"
	"github.com/example/go-ddd-template/contexts/inventory/internal/adapter/inbound/openapi"
	"github.com/example/go-ddd-template/contexts/inventory/internal/adapter/outbound/memory"
	"github.com/example/go-ddd-template/contexts/inventory/internal/application"
	"github.com/example/go-ddd-template/shared/correlation/corrhttp"
	"github.com/example/go-ddd-template/shared/uow"
)

// newServer はインメモリアダプタで組み立てた HTTP サーバを起動する。
// これにより OpenAPI → ogen サーバ → アプリケーション → ドメイン → リポジトリ の
// 一気通貫（walking skeleton の背骨）を HTTP レベルで検証できる。
func newServer(t *testing.T) *httptest.Server {
	t.Helper()
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	store := memory.NewStore()
	work := memory.NewUnitOfWork(store, memory.NewStores())
	read := memory.NewReadStockStore(store)
	exec := uow.NewExecutor(uow.WithBaseBackoff(0))
	dispatcher := application.NewInProcessDispatcher(log)

	replenisher := application.NewReplenisher(exec, work, dispatcher, log)
	viewer := application.NewStockViewer(read, log)

	h := httpapi.NewHandler(replenisher, viewer, log)
	// 本番の合成ルート（inventory.go）と同じヘルパーでオプションを渡す。ここを省くと
	// テストだけ ogen の既定エラーハンドラで動き、本番の振る舞いを検証できなくなる。
	server, err := openapi.NewServer(h, h.ServerOptions()...)
	require.NoError(t, err, "ogen サーバの構築")
	ts := httptest.NewServer(corrhttp.Middleware(server))
	t.Cleanup(ts.Close)
	return ts
}

func TestHTTP_ReplenishAndQuery(t *testing.T) {
	ts := newServer(t)
	client := ts.Client()

	// 補充。
	resp := postJSON(t, client, ts.URL+"/stock/WIDGET-001/replenish", `{"quantity":10}`)
	require.Equal(t, http.StatusOK, resp.StatusCode, "replenish ステータス")
	assert.NotEmpty(t, resp.Header.Get("X-Correlation-ID"), "レスポンスに X-Correlation-ID がある")
	var view struct {
		Sku       string `json:"sku"`
		Available int    `json:"available"`
		Reserved  int    `json:"reserved"`
		Version   int    `json:"version"`
	}
	decode(t, resp, &view)
	assert.Equal(t, "WIDGET-001", view.Sku)
	assert.Equal(t, 10, view.Available)
	assert.Equal(t, 1, view.Version)

	// 照会。
	resp = getURL(t, client, ts.URL+"/stock/WIDGET-001")
	require.Equal(t, http.StatusOK, resp.StatusCode, "query ステータス")
	decode(t, resp, &view)
	assert.Equal(t, 10, view.Available, "照会 available")
}

func TestHTTP_NotFoundProblemJSON(t *testing.T) {
	ts := newServer(t)
	resp := getURL(t, ts.Client(), ts.URL+"/stock/MISSING")
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
	assertProblemJSON(t, resp, http.StatusNotFound)
}

func TestHTTP_ValidationProblemJSON(t *testing.T) {
	ts := newServer(t)
	// 補充数量 0 はドメインで弾かれ、422 になる。
	resp := postJSON(t, ts.Client(), ts.URL+"/stock/WIDGET-001/replenish", `{"quantity":0}`)
	require.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
	assertProblemJSON(t, resp, http.StatusUnprocessableEntity)
}

// --- 相関 ID の回帰テスト（FR-5 / AC-3。公開 HTTP が共有ミドルウェア corrhttp を使う） ---

// TestHTTP_CorrelationInheritsTraceparent は、受信 traceparent の trace-id を相関 ID として
// 引き継ぎ、レスポンスヘッダ（X-Correlation-ID と traceparent）に反映することを確認する（経路 a/d）。
func TestHTTP_CorrelationInheritsTraceparent(t *testing.T) {
	const traceID = "0af7651916cd43dd8448eb211c80319c"
	ts := newServer(t)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, ts.URL+"/stock/WIDGET-001", nil)
	require.NoError(t, err, "リクエスト生成")
	req.Header.Set("traceparent", "00-"+traceID+"-b7ad6b7169203331-01")
	resp, err := ts.Client().Do(req)
	require.NoError(t, err, "送信")
	resp.Body.Close()
	assert.Equal(t, traceID, resp.Header.Get("X-Correlation-ID"), "traceparent の trace-id を引き継ぐ")
	assert.Contains(t, resp.Header.Get("traceparent"), traceID, "レスポンスに traceparent が載る")
}

// TestHTTP_CorrelationFromXCorrelationID は、traceparent が無く X-Correlation-ID のみの
// 場合にそれを引き継ぐことを確認する（経路 b）。
func TestHTTP_CorrelationFromXCorrelationID(t *testing.T) {
	ts := newServer(t)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, ts.URL+"/stock/WIDGET-001", nil)
	require.NoError(t, err, "リクエスト生成")
	req.Header.Set("X-Correlation-ID", "corr-xyz")
	resp, err := ts.Client().Do(req)
	require.NoError(t, err, "送信")
	resp.Body.Close()
	assert.Equal(t, "corr-xyz", resp.Header.Get("X-Correlation-ID"), "X-Correlation-ID を引き継ぐ")
	assert.Empty(t, resp.Header.Get("traceparent"), "32 桁 16 進でなければ traceparent は付かない")
}

// TestHTTP_CorrelationGeneratedWhenAbsent は、どちらのヘッダも無い場合に新規採番し、
// レスポンスに X-Correlation-ID と（32 桁 16 進なので）traceparent を載せることを確認する（経路 c/d）。
func TestHTTP_CorrelationGeneratedWhenAbsent(t *testing.T) {
	ts := newServer(t)
	resp := getURL(t, ts.Client(), ts.URL+"/stock/WIDGET-001")
	resp.Body.Close()
	cid := resp.Header.Get("X-Correlation-ID")
	require.NotEmpty(t, cid, "新規採番された相関 ID がレスポンスに載る")
	assert.Contains(t, resp.Header.Get("traceparent"), cid, "採番 ID は 32 桁 16 進なので traceparent も載る")
}

// --- ヘルパー ---

func postJSON(t *testing.T, c *http.Client, url, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, bytes.NewBufferString(body))
	require.NoError(t, err, "リクエスト生成")
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	require.NoError(t, err, "リクエスト送信")
	return resp
}

func getURL(t *testing.T, c *http.Client, url string) *http.Response {
	t.Helper()
	resp, err := c.Get(url)
	require.NoError(t, err, "GET")
	return resp
}

func decode(t *testing.T, resp *http.Response, v any) {
	t.Helper()
	defer resp.Body.Close()
	require.NoError(t, json.NewDecoder(resp.Body).Decode(v), "JSON デコード")
}

// assertProblemJSON は RFC 9457 の problem+json 応答を検証する。
func assertProblemJSON(t *testing.T, resp *http.Response, wantStatus int) {
	t.Helper()
	assert.Equal(t, "application/problem+json", resp.Header.Get("Content-Type"), "Content-Type")
	var pd struct {
		Type   string `json:"type"`
		Title  string `json:"title"`
		Status int    `json:"status"`
		Detail string `json:"detail"`
	}
	decode(t, resp, &pd)
	assert.Equal(t, wantStatus, pd.Status, "problem.status")
	assert.NotEmpty(t, pd.Title, "problem.title は空でない")
	assert.NotEmpty(t, pd.Type, "problem.type は空でない")
}
