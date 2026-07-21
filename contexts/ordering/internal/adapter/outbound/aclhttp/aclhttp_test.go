package aclhttp_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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
	if err != nil {
		t.Fatalf("クライアント生成失敗: %v", err)
	}
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
	if err != nil {
		t.Fatalf("想定外のエラー: %v", err)
	}

	// 送出されたリクエストが在庫内部 API の契約（ref + lines[sku,quantity]）に一致する。
	var got struct {
		Ref   string `json:"ref"`
		Lines []struct {
			Sku      string `json:"sku"`
			Quantity int    `json:"quantity"`
		} `json:"lines"`
	}
	if err := json.Unmarshal(captured.body, &got); err != nil {
		t.Fatalf("リクエスト本文のデコード失敗: %v (body=%s)", err, captured.body)
	}
	if got.Ref != "REF-1" || len(got.Lines) != 2 {
		t.Fatalf("リクエストが契約と不一致: %+v", got)
	}
	if got.Lines[0].Sku != "SKU-A" || got.Lines[0].Quantity != 3 {
		t.Fatalf("1 行目が不正: %+v", got.Lines[0])
	}
	if got.Lines[1].Sku != "SKU-B" || got.Lines[1].Quantity != 1 {
		t.Fatalf("2 行目が不正: %+v", got.Lines[1])
	}
}

func TestReserve_InsufficientStockTranslatesToRejected(t *testing.T) {
	ts, _ := stubInventory(t, http.StatusConflict, "application/problem+json", problemJSON, 0)
	r := newReserver(t, ts.URL, 5*time.Second)

	err := r.Reserve(context.Background(), "REF-1", []port.ReserveLine{{SKU: "SKU-A", Qty: 3}})
	if !errors.Is(err, application.ErrReservationRejected) {
		t.Fatalf("エラー = %v, want ErrReservationRejected", err)
	}
	// 業務的拒否（409）は不達（503）ではない。
	if errors.Is(err, application.ErrReservationUnavailable) {
		t.Fatalf("409 が不達（Unavailable）に翻訳された: %v", err)
	}
}

func TestReserve_ServerErrorTranslatesToUnavailable(t *testing.T) {
	ts, _ := stubInventory(t, http.StatusServiceUnavailable, "application/problem+json", problemJSON503, 0)
	r := newReserver(t, ts.URL, 5*time.Second)

	err := r.Reserve(context.Background(), "REF-1", []port.ReserveLine{{SKU: "SKU-A", Qty: 3}})
	// 5xx は不達（503）かつ拒否（両番兵に一致）。
	if !errors.Is(err, application.ErrReservationUnavailable) {
		t.Fatalf("エラー = %v, want ErrReservationUnavailable", err)
	}
	if !errors.Is(err, application.ErrReservationRejected) {
		t.Fatalf("エラー = %v, want ErrReservationRejected も満たすこと", err)
	}
}

func TestReserve_TimeoutTranslatesToUnavailable(t *testing.T) {
	// スタブはクライアントのタイムアウトより長く遅延する。
	ts, _ := stubInventory(t, http.StatusOK, "application/json", okAck, 200*time.Millisecond)
	r := newReserver(t, ts.URL, 30*time.Millisecond)

	err := r.Reserve(context.Background(), "REF-1", []port.ReserveLine{{SKU: "SKU-A", Qty: 3}})
	if !errors.Is(err, application.ErrReservationUnavailable) {
		t.Fatalf("エラー = %v, want ErrReservationUnavailable", err)
	}
}
