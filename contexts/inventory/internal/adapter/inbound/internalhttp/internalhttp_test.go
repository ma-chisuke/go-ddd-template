package internalhttp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/example/go-ddd-template/contexts/inventory/internal/adapter/inbound/internalhttp"
	"github.com/example/go-ddd-template/contexts/inventory/internal/adapter/inbound/openapiinternal"
	"github.com/example/go-ddd-template/contexts/inventory/internal/adapter/outbound/memory"
	"github.com/example/go-ddd-template/contexts/inventory/internal/application"
	"github.com/example/go-ddd-template/shared/outbox"
	"github.com/example/go-ddd-template/shared/uow"
)

func newInternalServer(t *testing.T) *httptest.Server {
	t.Helper()
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	store := memory.NewStore()
	work := memory.NewUnitOfWork(store, memory.NewOutboxStore())
	exec := uow.NewExecutor(uow.WithBaseBackoff(0))
	dispatcher := application.NewInProcessDispatcher(log)

	// 内部エンドポイントのテストには在庫が必要なので、補充ユースケースで種を蒔く。
	replenisher := application.NewReplenisher(exec, work, dispatcher, log)
	if _, err := replenisher.Replenish(context.Background(), application.ReplenishInput{SKU: "WIDGET-001", Quantity: 10}); err != nil {
		t.Fatalf("補充失敗: %v", err)
	}

	reserver := application.NewReserver(exec, work, dispatcher, log, 0)
	confirmer := application.NewConfirmer(exec, work, dispatcher, log)
	releaser := application.NewReleaser(exec, work, dispatcher, log)

	router := outbox.NewRouter()
	router.Register(application.MessageTypeConfirmReservation, application.OnConfirmReservation(confirmer, log))
	router.Register(application.MessageTypeOrderCancelled, application.OnOrderCancelled(releaser))

	h := internalhttp.NewHandler(reserver, confirmer, releaser, router, log)
	server, err := openapiinternal.NewServer(h)
	if err != nil {
		t.Fatalf("内部サーバの構築に失敗: %v", err)
	}
	ts := httptest.NewServer(server)
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

func TestInternalHTTP_ReserveConfirmRelease(t *testing.T) {
	ts := newInternalServer(t)
	c := ts.Client()

	// 予約。
	resp := post(t, c, ts.URL+"/reservations", `{"ref":"ORDER-1","lines":[{"sku":"WIDGET-001","quantity":4}]}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reserve ステータス = %d, want 200", resp.StatusCode)
	}
	resp.Body.Close()

	// 確定。
	resp = post(t, c, ts.URL+"/reservations/ORDER-1/confirm", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("confirm ステータス = %d, want 200", resp.StatusCode)
	}
	resp.Body.Close()

	// 解放。
	resp = post(t, c, ts.URL+"/reservations/ORDER-1/release", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("release ステータス = %d, want 200", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestInternalHTTP_ReserveInsufficientIsConflict(t *testing.T) {
	ts := newInternalServer(t)
	resp := post(t, ts.Client(), ts.URL+"/reservations", `{"ref":"ORDER-1","lines":[{"sku":"WIDGET-001","quantity":999}]}`)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("ステータス = %d, want 409", resp.StatusCode)
	}
	assertProblemJSON(t, resp, http.StatusConflict)
}

func TestInternalHTTP_ConfirmUnknownIsNotFound(t *testing.T) {
	ts := newInternalServer(t)
	resp := post(t, ts.Client(), ts.URL+"/reservations/NEVER/confirm", "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("ステータス = %d, want 404", resp.StatusCode)
	}
	assertProblemJSON(t, resp, http.StatusNotFound)
}

func TestInternalHTTP_IngestEventRoutesToRelease(t *testing.T) {
	ts := newInternalServer(t)
	c := ts.Client()

	// まず予約する。
	resp := post(t, c, ts.URL+"/reservations", `{"ref":"ORDER-1","lines":[{"sku":"WIDGET-001","quantity":4}]}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reserve ステータス = %d, want 200", resp.StatusCode)
	}
	resp.Body.Close()

	// 取消イベントを取り込む → Release に振り分けられる。
	body := `{"id":"m-1","type":"ordering.order.cancelled","payload":"{\"reservation_ref\":\"ORDER-1\"}","trace_id":"t-1"}`
	resp = post(t, c, ts.URL+"/events", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("event-ingest ステータス = %d, want 200", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestInternalHTTP_IngestUnknownTypeIsUnprocessable(t *testing.T) {
	ts := newInternalServer(t)
	body := `{"id":"m-1","type":"unknown.type","payload":"{}"}`
	resp := post(t, ts.Client(), ts.URL+"/events", body)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("ステータス = %d, want 422", resp.StatusCode)
	}
	assertProblemJSON(t, resp, http.StatusUnprocessableEntity)
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
