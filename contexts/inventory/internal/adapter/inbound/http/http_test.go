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
	"github.com/example/go-ddd-template/shared/uow"
)

// newServer はインメモリアダプタで組み立てた HTTP サーバを起動する。
// これにより OpenAPI → ogen サーバ → アプリケーション → ドメイン → リポジトリ の
// 一気通貫（walking skeleton の背骨）を HTTP レベルで検証できる。
func newServer(t *testing.T) *httptest.Server {
	t.Helper()
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	store := memory.NewStore()
	work := memory.NewUnitOfWork(store, memory.NewOutboxStore(), memory.NewEventStore())
	read := memory.NewReadStockStore(store)
	exec := uow.NewExecutor(uow.WithBaseBackoff(0))
	dispatcher := application.NewInProcessDispatcher(log)

	replenisher := application.NewReplenisher(exec, work, dispatcher, log)
	viewer := application.NewStockViewer(read, log)

	h := httpapi.NewHandler(replenisher, viewer, log)
	server, err := openapi.NewServer(h)
	require.NoError(t, err, "ogen サーバの構築")
	ts := httptest.NewServer(httpapi.CorrelationMiddleware(server))
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
