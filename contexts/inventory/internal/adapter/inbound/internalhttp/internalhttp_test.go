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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
	work := memory.NewUnitOfWork(store, memory.NewOutboxStore(), memory.NewEventStore())
	exec := uow.NewExecutor(uow.WithBaseBackoff(0))
	dispatcher := application.NewInProcessDispatcher(log)

	// 内部エンドポイントのテストには在庫が必要なので、補充ユースケースで種を蒔く。
	replenisher := application.NewReplenisher(exec, work, dispatcher, log)
	_, err := replenisher.Replenish(context.Background(), application.ReplenishInput{SKU: "WIDGET-001", Quantity: 10})
	require.NoError(t, err, "補充")

	reserver := application.NewReserver(exec, work, dispatcher, log, 0)
	confirmer := application.NewConfirmer(exec, work, dispatcher, log)
	releaser := application.NewReleaser(exec, work, dispatcher, log)

	router := outbox.NewRouter()
	router.Register(application.MessageTypeConfirmReservation, application.OnConfirmReservation(confirmer, log))
	router.Register(application.MessageTypeOrderCancelled, application.OnOrderCancelled(releaser))

	h := internalhttp.NewHandler(reserver, confirmer, releaser, router, log)
	server, err := openapiinternal.NewServer(h)
	require.NoError(t, err, "内部サーバの構築")
	ts := httptest.NewServer(server)
	t.Cleanup(ts.Close)
	return ts
}

func post(t *testing.T, c *http.Client, url, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, bytes.NewBufferString(body))
	require.NoError(t, err, "リクエスト生成")
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	require.NoError(t, err, "送信")
	return resp
}

func TestInternalHTTP_ReserveConfirmRelease(t *testing.T) {
	ts := newInternalServer(t)
	c := ts.Client()

	// 予約。
	resp := post(t, c, ts.URL+"/reservations", `{"ref":"ORDER-1","lines":[{"sku":"WIDGET-001","quantity":4}]}`)
	require.Equal(t, http.StatusOK, resp.StatusCode, "reserve ステータス")
	resp.Body.Close()

	// 確定。
	resp = post(t, c, ts.URL+"/reservations/ORDER-1/confirm", "")
	require.Equal(t, http.StatusOK, resp.StatusCode, "confirm ステータス")
	resp.Body.Close()

	// 解放。
	resp = post(t, c, ts.URL+"/reservations/ORDER-1/release", "")
	require.Equal(t, http.StatusOK, resp.StatusCode, "release ステータス")
	resp.Body.Close()
}

func TestInternalHTTP_ReserveInsufficientIsConflict(t *testing.T) {
	ts := newInternalServer(t)
	resp := post(t, ts.Client(), ts.URL+"/reservations", `{"ref":"ORDER-1","lines":[{"sku":"WIDGET-001","quantity":999}]}`)
	require.Equal(t, http.StatusConflict, resp.StatusCode)
	assertProblemJSON(t, resp, http.StatusConflict)
}

func TestInternalHTTP_ConfirmUnknownIsNotFound(t *testing.T) {
	ts := newInternalServer(t)
	resp := post(t, ts.Client(), ts.URL+"/reservations/NEVER/confirm", "")
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
	assertProblemJSON(t, resp, http.StatusNotFound)
}

func TestInternalHTTP_IngestEventRoutesToRelease(t *testing.T) {
	ts := newInternalServer(t)
	c := ts.Client()

	// まず予約する。
	resp := post(t, c, ts.URL+"/reservations", `{"ref":"ORDER-1","lines":[{"sku":"WIDGET-001","quantity":4}]}`)
	require.Equal(t, http.StatusOK, resp.StatusCode, "reserve ステータス")
	resp.Body.Close()

	// 取消イベントを取り込む → Release に振り分けられる。
	body := `{"id":"m-1","type":"ordering.order.cancelled","payload":"{\"reservation_ref\":\"ORDER-1\"}","trace_id":"t-1"}`
	resp = post(t, c, ts.URL+"/events", body)
	require.Equal(t, http.StatusOK, resp.StatusCode, "event-ingest ステータス")
	resp.Body.Close()
}

func TestInternalHTTP_IngestUnknownTypeIsUnprocessable(t *testing.T) {
	ts := newInternalServer(t)
	body := `{"id":"m-1","type":"unknown.type","payload":"{}"}`
	resp := post(t, ts.Client(), ts.URL+"/events", body)
	require.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
	assertProblemJSON(t, resp, http.StatusUnprocessableEntity)
}

// assertProblemJSON は RFC 9457 の problem+json 応答を検証する。
func assertProblemJSON(t *testing.T, resp *http.Response, wantStatus int) {
	t.Helper()
	defer resp.Body.Close()
	assert.Equal(t, "application/problem+json", resp.Header.Get("Content-Type"), "Content-Type")
	var pd struct {
		Type   string `json:"type"`
		Title  string `json:"title"`
		Status int    `json:"status"`
		Detail string `json:"detail"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&pd), "JSON デコード")
	assert.Equal(t, wantStatus, pd.Status, "problem.status")
	assert.NotEmpty(t, pd.Title, "problem.title は空でない")
	assert.NotEmpty(t, pd.Type, "problem.type は空でない")
}
