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
	"github.com/example/go-ddd-template/contexts/inventory/internal/domain"
	"github.com/example/go-ddd-template/shared/correlation/corrhttp"
	"github.com/example/go-ddd-template/shared/event"
	"github.com/example/go-ddd-template/shared/outbox"
	"github.com/example/go-ddd-template/shared/uow"
)

// newInternalHandler は本番の合成ルート（inventory.go）と同じ結線で内部 HTTP ハンドラを
// 組み立てる。在庫が必要なので補充ユースケースで種を蒔いてある。
//
// httptest サーバを起こす newInternalServer と、プロセス内で直接 ServeHTTP を呼ぶ fuzz
// ターゲット（internalhttp_fuzz_test.go）の両方がここを使う。fuzz がサーバを経由しないのは、
// net/http のサーバがハンドラの panic を**回復してログへ落とす**ため、サーバ越しでは panic が
// テストの失敗として現れないからである（「panic しない」を主張できなくなる）。
func newInternalHandler(t *testing.T) http.Handler {
	t.Helper()
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	rows := memory.NewStockItemRows()
	work := memory.NewUnitOfWork(rows, memory.NewStores())
	exec := uow.NewExecutor(uow.WithBaseBackoff(0))
	dispatcher := event.NewTyped[domain.DomainEvent](log)

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
	// 本番の合成ルート（inventory.go）と同じヘルパーでオプションを渡す。ここを省くと
	// テストだけ ogen の既定エラーハンドラで動き、本番の振る舞いを検証できなくなる。
	server, err := openapiinternal.NewServer(h, h.ServerOptions()...)
	require.NoError(t, err, "内部サーバの構築")
	// 本番結線（inventory.go）と同じく、内部サーバも共有ミドルウェア corrhttp で相関 ID を
	// 確立する。これにより注文サービスから伝播した traceparent が最終ホップまで途切れない。
	return corrhttp.Middleware(server)
}

func newInternalServer(t *testing.T) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(newInternalHandler(t))
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

// --- 相関 ID の回帰テスト（FR-5 / AC-3。内部 HTTP が共有ミドルウェア corrhttp を使う。
//     注文 -> 在庫の内部 HTTP という実運用の相関経路はこちらが本命） ---

// TestInternalHTTP_CorrelationInheritsTraceparent は、内部 HTTP が受信 traceparent の
// trace-id を相関 ID として引き継ぎ、レスポンスヘッダにも反映することを確認する（経路 a/d）。
// これが FR-5 の是正点（従来は内部サーバが traceparent を読まず相関が切れていた）。
func TestInternalHTTP_CorrelationInheritsTraceparent(t *testing.T) {
	const traceID = "0af7651916cd43dd8448eb211c80319c"
	ts := newInternalServer(t)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, ts.URL+"/reservations/NEVER/confirm", nil)
	require.NoError(t, err, "リクエスト生成")
	req.Header.Set("traceparent", "00-"+traceID+"-b7ad6b7169203331-01")
	resp, err := ts.Client().Do(req)
	require.NoError(t, err, "送信")
	resp.Body.Close()
	assert.Equal(t, traceID, resp.Header.Get("X-Correlation-ID"), "内部 HTTP も traceparent の trace-id を引き継ぐ")
	assert.Contains(t, resp.Header.Get("traceparent"), traceID, "レスポンスに traceparent が載る")
}

// TestInternalHTTP_CorrelationFromXCorrelationID は、traceparent が無く X-Correlation-ID
// のみの場合にそれを引き継ぐことを確認する（経路 b）。
func TestInternalHTTP_CorrelationFromXCorrelationID(t *testing.T) {
	ts := newInternalServer(t)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, ts.URL+"/reservations/NEVER/confirm", nil)
	require.NoError(t, err, "リクエスト生成")
	req.Header.Set("X-Correlation-ID", "corr-internal")
	resp, err := ts.Client().Do(req)
	require.NoError(t, err, "送信")
	resp.Body.Close()
	assert.Equal(t, "corr-internal", resp.Header.Get("X-Correlation-ID"), "X-Correlation-ID を引き継ぐ")
	assert.Empty(t, resp.Header.Get("traceparent"), "32 桁 16 進でなければ traceparent は付かない")
}

// TestInternalHTTP_CorrelationGeneratedWhenAbsent は、どちらのヘッダも無い場合に新規採番し、
// レスポンスに X-Correlation-ID と（32 桁 16 進なので）traceparent を載せることを確認する（経路 c/d）。
func TestInternalHTTP_CorrelationGeneratedWhenAbsent(t *testing.T) {
	ts := newInternalServer(t)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, ts.URL+"/reservations/NEVER/confirm", nil)
	require.NoError(t, err, "リクエスト生成")
	resp, err := ts.Client().Do(req)
	require.NoError(t, err, "送信")
	resp.Body.Close()
	cid := resp.Header.Get("X-Correlation-ID")
	require.NotEmpty(t, cid, "新規採番された相関 ID がレスポンスに載る")
	assert.Contains(t, resp.Header.Get("traceparent"), cid, "採番 ID は 32 桁 16 進なので traceparent も載る")
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
