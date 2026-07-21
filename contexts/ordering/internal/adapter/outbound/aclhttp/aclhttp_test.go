package aclhttp_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/example/go-ddd-template/contexts/ordering/internal/adapter/outbound/aclhttp"
	"github.com/example/go-ddd-template/contexts/ordering/internal/application"
	"github.com/example/go-ddd-template/contexts/ordering/port"
)

// capturedRequest は在庫内部 API のスタブが受け取ったリクエストの記録。
type capturedRequest struct {
	body    []byte
	headers http.Header
}

// stubInventory は在庫の内部 API（/reservations）を模す httptest サーバを起動する。
// respond で応答（ステータス・Content-Type・本文）を差し替え、delay で遅延を注入できる。
func stubInventory(t *testing.T, status int, contentType, body string, delay time.Duration) (*httptest.Server, *capturedRequest) {
	t.Helper()
	captured := &capturedRequest{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /reservations", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		captured.body = b
		captured.headers = r.Header.Clone()
		if delay > 0 {
			time.Sleep(delay)
		}
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, captured
}

func newReserver(t *testing.T, baseURL string, timeout time.Duration) *aclhttp.Reserver {
	t.Helper()
	client, err := aclhttp.NewInventoryClient(baseURL, timeout)
	require.NoError(t, err, "クライアント生成失敗")
	return aclhttp.NewReserver(client)
}

const okAck = `{"status":"reserved"}`

const problemJSON = `{"type":"about:blank","title":"Conflict","status":409,"detail":"在庫が不足しています"}`

const problemJSON503 = `{"type":"about:blank","title":"Service Unavailable","status":503,"detail":"利用不可"}`

func TestReserve_MapsRequestToClientContract(t *testing.T) {
	ts, captured := stubInventory(t, http.StatusOK, "application/json", okAck, 0)
	r := newReserver(t, ts.URL, 5*time.Second)

	err := r.Reserve(context.Background(), "REF-1", []port.ReserveLine{
		{SKU: "SKU-A", Qty: 3},
		{SKU: "SKU-B", Qty: 1},
	})
	require.NoError(t, err)

	// 送出されたリクエストが在庫内部 API の契約（ref + lines[sku,quantity]）に一致する。
	var got struct {
		Ref   string `json:"ref"`
		Lines []struct {
			Sku      string `json:"sku"`
			Quantity int    `json:"quantity"`
		} `json:"lines"`
	}
	require.NoError(t, json.Unmarshal(captured.body, &got), "リクエスト本文のデコード失敗 (body=%s)", captured.body)
	assert.Equal(t, "REF-1", got.Ref)
	require.Len(t, got.Lines, 2)
	assert.Equal(t, "SKU-A", got.Lines[0].Sku)
	assert.Equal(t, 3, got.Lines[0].Quantity)
	assert.Equal(t, "SKU-B", got.Lines[1].Sku)
	assert.Equal(t, 1, got.Lines[1].Quantity)
}

func TestReserve_InsufficientStockTranslatesToRejected(t *testing.T) {
	ts, _ := stubInventory(t, http.StatusConflict, "application/problem+json", problemJSON, 0)
	r := newReserver(t, ts.URL, 5*time.Second)

	err := r.Reserve(context.Background(), "REF-1", []port.ReserveLine{{SKU: "SKU-A", Qty: 3}})
	require.ErrorIs(t, err, application.ErrReservationRejected)
	// 業務的拒否（409）は不達（503）ではない。
	assert.NotErrorIs(t, err, application.ErrReservationUnavailable, "409 が不達（Unavailable）に翻訳された")
}

func TestReserve_ServerErrorTranslatesToUnavailable(t *testing.T) {
	ts, _ := stubInventory(t, http.StatusServiceUnavailable, "application/problem+json", problemJSON503, 0)
	r := newReserver(t, ts.URL, 5*time.Second)

	err := r.Reserve(context.Background(), "REF-1", []port.ReserveLine{{SKU: "SKU-A", Qty: 3}})
	// 5xx は不達（503）かつ拒否（両番兵に一致）。
	require.ErrorIs(t, err, application.ErrReservationUnavailable)
	assert.ErrorIs(t, err, application.ErrReservationRejected, "5xx は ErrReservationRejected も満たすべき")
}

func TestReserve_TimeoutTranslatesToUnavailable(t *testing.T) {
	// スタブはクライアントのタイムアウトより長く遅延する。
	ts, _ := stubInventory(t, http.StatusOK, "application/json", okAck, 200*time.Millisecond)
	r := newReserver(t, ts.URL, 30*time.Millisecond)

	err := r.Reserve(context.Background(), "REF-1", []port.ReserveLine{{SKU: "SKU-A", Qty: 3}})
	require.ErrorIs(t, err, application.ErrReservationUnavailable)
}
