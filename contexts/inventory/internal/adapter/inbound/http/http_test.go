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
	work := memory.NewUnitOfWork(store)
	read := memory.NewReadStockStore(store)
	exec := uow.NewExecutor(uow.WithBaseBackoff(0))
	dispatcher := application.NewInProcessDispatcher(log)

	replenisher := application.NewReplenisher(exec, work, dispatcher, log)
	viewer := application.NewStockViewer(read, log)

	h := httpapi.NewHandler(replenisher, viewer, log)
	server, err := openapi.NewServer(h)
	if err != nil {
		t.Fatalf("ogen サーバの構築に失敗: %v", err)
	}
	ts := httptest.NewServer(httpapi.CorrelationMiddleware(server))
	t.Cleanup(ts.Close)
	return ts
}

func TestHTTP_ReplenishAndQuery(t *testing.T) {
	ts := newServer(t)
	client := ts.Client()

	// 補充。
	resp := postJSON(t, client, ts.URL+"/stock/WIDGET-001/replenish", `{"quantity":10}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("replenish ステータス = %d, want 200", resp.StatusCode)
	}
	if cid := resp.Header.Get("X-Correlation-ID"); cid == "" {
		t.Fatal("レスポンスに X-Correlation-ID が無い")
	}
	var view struct {
		Sku       string `json:"sku"`
		Available int    `json:"available"`
		Reserved  int    `json:"reserved"`
		Version   int    `json:"version"`
	}
	decode(t, resp, &view)
	if view.Available != 10 || view.Version != 1 || view.Sku != "WIDGET-001" {
		t.Fatalf("補充応答が不正: %+v", view)
	}

	// 照会。
	resp = getURL(t, client, ts.URL+"/stock/WIDGET-001")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("query ステータス = %d, want 200", resp.StatusCode)
	}
	decode(t, resp, &view)
	if view.Available != 10 {
		t.Fatalf("照会応答が不正: %+v", view)
	}
}

func TestHTTP_NotFoundProblemJSON(t *testing.T) {
	ts := newServer(t)
	resp := getURL(t, ts.Client(), ts.URL+"/stock/MISSING")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("ステータス = %d, want 404", resp.StatusCode)
	}
	assertProblemJSON(t, resp, http.StatusNotFound)
}

func TestHTTP_ValidationProblemJSON(t *testing.T) {
	ts := newServer(t)
	// 補充数量 0 はドメインで弾かれ、422 になる。
	resp := postJSON(t, ts.Client(), ts.URL+"/stock/WIDGET-001/replenish", `{"quantity":0}`)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("ステータス = %d, want 422", resp.StatusCode)
	}
	assertProblemJSON(t, resp, http.StatusUnprocessableEntity)
}

// --- ヘルパー ---

func postJSON(t *testing.T, c *http.Client, url, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("リクエスト生成失敗: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("リクエスト送信失敗: %v", err)
	}
	return resp
}

func getURL(t *testing.T, c *http.Client, url string) *http.Response {
	t.Helper()
	resp, err := c.Get(url)
	if err != nil {
		t.Fatalf("GET 失敗: %v", err)
	}
	return resp
}

func decode(t *testing.T, resp *http.Response, v any) {
	t.Helper()
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatalf("JSON デコード失敗: %v", err)
	}
}

// assertProblemJSON は RFC 9457 の problem+json 応答を検証する。
func assertProblemJSON(t *testing.T, resp *http.Response, wantStatus int) {
	t.Helper()
	if ct := resp.Header.Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("Content-Type = %q, want application/problem+json", ct)
	}
	var pd struct {
		Type   string `json:"type"`
		Title  string `json:"title"`
		Status int    `json:"status"`
		Detail string `json:"detail"`
	}
	decode(t, resp, &pd)
	if pd.Status != wantStatus {
		t.Fatalf("problem.status = %d, want %d", pd.Status, wantStatus)
	}
	if pd.Title == "" || pd.Type == "" {
		t.Fatalf("problem の必須項目が空: %+v", pd)
	}
}
