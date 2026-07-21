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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	httpapi "github.com/example/go-ddd-template/contexts/ordering/internal/adapter/inbound/http"
	"github.com/example/go-ddd-template/contexts/ordering/internal/adapter/inbound/openapi"
	"github.com/example/go-ddd-template/contexts/ordering/internal/adapter/outbound/memory"
	"github.com/example/go-ddd-template/contexts/ordering/internal/application"
	"github.com/example/go-ddd-template/contexts/ordering/port"
	"github.com/example/go-ddd-template/shared/uow"
)

// これは HTTP 継ぎ目（seam）の統合テスト。本物のハンドラ・生成サーバ（ogen）・本物の
// インメモリアダプタを httptest で結線し、入口から出口までを通しで検証する。
//
// stubReserver は在庫予約 ACL ポートの「本物のスタブ」（gomock のモックではない）。継ぎ目
// テストでは境界の入力（成功／各種番兵）を固定するだけでよいので、呼び出し回数や順序を縛る
// gomock ではなく単純なスタブを使う。ポート単体の相互作用検証は application 層のテストで
// gomock により別途行う（gomock は継ぎ目テストに対して加算的であり、置き換えではない）。
type stubReserver struct{ err error }

func (s stubReserver) Reserve(_ context.Context, _ string, _ []port.ReserveLine) error { return s.err }
func (s stubReserver) Release(_ context.Context, _ string) error                       { return nil }

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
	require.NoError(t, err, "サーバ構築に失敗")
	ts := httptest.NewServer(httpapi.CorrelationMiddleware(server))
	t.Cleanup(ts.Close)
	return ts
}

func post(t *testing.T, c *http.Client, url, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, bytes.NewBufferString(body))
	require.NoError(t, err, "リクエスト生成失敗")
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	require.NoError(t, err, "送信失敗")
	return resp
}

const placeBody = `{"customerId":"CUST-1","lines":[{"sku":"SKU-A","quantity":3,"unitPrice":{"amount":1200,"currency":"JPY"}}]}`

func TestHTTP_PlaceGetCancel(t *testing.T) {
	ts := newServer(t, stubReserver{})
	c := ts.Client()

	// 作成 -> 201。
	resp := post(t, c, ts.URL+"/orders", placeBody)
	require.Equal(t, http.StatusCreated, resp.StatusCode, "place ステータス")
	var created struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		Total  struct {
			Amount   int64  `json:"amount"`
			Currency string `json:"currency"`
		} `json:"total"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
	resp.Body.Close()
	assert.Equal(t, "confirmed", created.Status)
	assert.Equal(t, int64(3600), created.Total.Amount)

	// 照会 -> 200。
	getResp, err := c.Get(ts.URL + "/orders/" + created.ID)
	require.NoError(t, err, "照会失敗")
	assert.Equal(t, http.StatusOK, getResp.StatusCode, "get ステータス")
	getResp.Body.Close()

	// 取消 -> 200・cancelled。
	cancelResp := post(t, c, ts.URL+"/orders/"+created.ID+"/cancel", "")
	require.Equal(t, http.StatusOK, cancelResp.StatusCode, "cancel ステータス")
	var cancelled struct {
		Status string `json:"status"`
	}
	require.NoError(t, json.NewDecoder(cancelResp.Body).Decode(&cancelled))
	cancelResp.Body.Close()
	assert.Equal(t, "cancelled", cancelled.Status)
}

func TestHTTP_ReserveRejectedIsConflict(t *testing.T) {
	ts := newServer(t, stubReserver{err: application.ErrReservationRejected})
	resp := post(t, ts.Client(), ts.URL+"/orders", placeBody)
	require.Equal(t, http.StatusConflict, resp.StatusCode)
	assertProblemJSON(t, resp, http.StatusConflict)
}

func TestHTTP_ReserveUnavailableIsServiceUnavailable(t *testing.T) {
	ts := newServer(t, stubReserver{err: errors.Join(application.ErrReservationRejected, application.ErrReservationUnavailable)})
	resp := post(t, ts.Client(), ts.URL+"/orders", placeBody)
	require.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
	assertProblemJSON(t, resp, http.StatusServiceUnavailable)
}

func TestHTTP_EmptyLinesIsUnprocessable(t *testing.T) {
	ts := newServer(t, stubReserver{})
	resp := post(t, ts.Client(), ts.URL+"/orders", `{"customerId":"CUST-1","lines":[]}`)
	require.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
	assertProblemJSON(t, resp, http.StatusUnprocessableEntity)
}

func TestHTTP_GetUnknownIsNotFound(t *testing.T) {
	ts := newServer(t, stubReserver{})
	resp, err := ts.Client().Get(ts.URL + "/orders/NOPE")
	require.NoError(t, err, "照会失敗")
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
	assertProblemJSON(t, resp, http.StatusNotFound)
}

// assertProblemJSON は RFC 9457 の problem+json 応答を検証する。
func assertProblemJSON(t *testing.T, resp *http.Response, wantStatus int) {
	t.Helper()
	defer resp.Body.Close()
	assert.Equal(t, "application/problem+json", resp.Header.Get("Content-Type"))
	var pd struct {
		Type   string `json:"type"`
		Title  string `json:"title"`
		Status int    `json:"status"`
		Detail string `json:"detail"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&pd))
	assert.Equal(t, wantStatus, pd.Status, "problem.status")
	assert.NotEmpty(t, pd.Title, "problem.title は必須")
	assert.NotEmpty(t, pd.Type, "problem.type は必須")
}
