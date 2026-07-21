package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	httpapi "github.com/example/go-ddd-template/contexts/ordering/internal/adapter/inbound/http"
	"github.com/example/go-ddd-template/contexts/ordering/internal/adapter/inbound/openapi"
	"github.com/example/go-ddd-template/contexts/ordering/internal/adapter/outbound/memory"
	"github.com/example/go-ddd-template/contexts/ordering/internal/application"
	"github.com/example/go-ddd-template/contexts/ordering/port"
	"github.com/example/go-ddd-template/shared/uow"
)

// fakeReserver は StockReserver の差し替え（注入したエラーを返す）。
type fakeReserver struct{ err error }

func (f fakeReserver) Reserve(_ context.Context, _ string, _ []port.ReserveLine) error { return f.err }
func (f fakeReserver) Release(_ context.Context, _ string) error                       { return nil }

func newServer(t *testing.T, reserver application.StockReserver) *httptest.Server {
	t.Helper()
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	store := memory.NewStore()
	obx := memory.NewOutboxStore()
	work := memory.NewUnitOfWork(store, obx)
	exec := uow.NewExecutor(uow.WithBaseBackoff(0))
	dispatcher := application.NewInProcessDispatcher(log)

	place := application.NewPlaceOrder(exec, work, reserver, dispatcher, log)
	get := application.NewGetOrder(memory.NewReadOrderStore(store), log)
	cancel := application.NewCancelOrder(exec, work, log)

	h := httpapi.NewHandler(place, get, cancel, log)
	server, err := openapi.NewServer(h)
	if err != nil {
		t.Fatalf("サーバ構築に失敗: %v", err)
	}
	ts := httptest.NewServer(httpapi.CorrelationMiddleware(server))
	t.Cleanup(ts.Close)
	return ts
}

func post(t *testing.T, c *http.Client, url, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("リクエスト生成失敗: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("送信失敗: %v", err)
	}
	return resp
}

const placeBody = `{"customerId":"CUST-1","lines":[{"sku":"SKU-A","quantity":3,"unitPrice":{"amount":1200,"currency":"JPY"}}]}`

func TestHTTP_PlaceGetCancel(t *testing.T) {
	ts := newServer(t, fakeReserver{})
	c := ts.Client()

	// 作成 -> 201。
	resp := post(t, c, ts.URL+"/orders", placeBody)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("place ステータス = %d, want 201", resp.StatusCode)
	}
	var created struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		Total  struct {
			Amount   int64  `json:"amount"`
			Currency string `json:"currency"`
		} `json:"total"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("JSON デコード失敗: %v", err)
	}
	resp.Body.Close()
	if created.Status != "confirmed" || created.Total.Amount != 3600 {
		t.Fatalf("作成結果が不正: %+v", created)
	}

	// 照会 -> 200。
	getResp, err := c.Get(ts.URL + "/orders/" + created.ID)
	if err != nil {
		t.Fatalf("照会失敗: %v", err)
	}
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("get ステータス = %d, want 200", getResp.StatusCode)
	}
	getResp.Body.Close()

	// 取消 -> 200・cancelled。
	cancelResp := post(t, c, ts.URL+"/orders/"+created.ID+"/cancel", "")
	if cancelResp.StatusCode != http.StatusOK {
		t.Fatalf("cancel ステータス = %d, want 200", cancelResp.StatusCode)
	}
	var cancelled struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(cancelResp.Body).Decode(&cancelled); err != nil {
		t.Fatalf("JSON デコード失敗: %v", err)
	}
	cancelResp.Body.Close()
	if cancelled.Status != "cancelled" {
		t.Fatalf("取消後の状態 = %q, want cancelled", cancelled.Status)
	}
}

func TestHTTP_ReserveRejectedIsConflict(t *testing.T) {
	ts := newServer(t, fakeReserver{err: application.ErrReservationRejected})
	resp := post(t, ts.Client(), ts.URL+"/orders", placeBody)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("ステータス = %d, want 409", resp.StatusCode)
	}
	assertProblemJSON(t, resp, http.StatusConflict)
}

func TestHTTP_ReserveUnavailableIsServiceUnavailable(t *testing.T) {
	ts := newServer(t, fakeReserver{err: errors.Join(application.ErrReservationRejected, application.ErrReservationUnavailable)})
	resp := post(t, ts.Client(), ts.URL+"/orders", placeBody)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("ステータス = %d, want 503", resp.StatusCode)
	}
	assertProblemJSON(t, resp, http.StatusServiceUnavailable)
}

func TestHTTP_EmptyLinesIsUnprocessable(t *testing.T) {
	ts := newServer(t, fakeReserver{})
	resp := post(t, ts.Client(), ts.URL+"/orders", `{"customerId":"CUST-1","lines":[]}`)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("ステータス = %d, want 422", resp.StatusCode)
	}
	assertProblemJSON(t, resp, http.StatusUnprocessableEntity)
}

func TestHTTP_GetUnknownIsNotFound(t *testing.T) {
	ts := newServer(t, fakeReserver{})
	resp, err := ts.Client().Get(ts.URL + "/orders/NOPE")
	if err != nil {
		t.Fatalf("照会失敗: %v", err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("ステータス = %d, want 404", resp.StatusCode)
	}
	assertProblemJSON(t, resp, http.StatusNotFound)
}

// assertProblemJSON は RFC 9457 の problem+json 応答を検証する。
func assertProblemJSON(t *testing.T, resp *http.Response, wantStatus int) {
	t.Helper()
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("Content-Type = %q, want application/problem+json", ct)
	}
	var pd struct {
		Type   string `json:"type"`
		Title  string `json:"title"`
		Status int    `json:"status"`
		Detail string `json:"detail"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&pd); err != nil {
		t.Fatalf("JSON デコード失敗: %v", err)
	}
	if pd.Status != wantStatus {
		t.Fatalf("problem.status = %d, want %d", pd.Status, wantStatus)
	}
	if pd.Title == "" || pd.Type == "" {
		t.Fatalf("problem の必須項目が空: %+v", pd)
	}
}
