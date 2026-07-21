package application

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/example/go-ddd-template/contexts/ordering/internal/domain/order"
)

// これはパッケージ内部（application）テスト。非公開のメッセージ組み立て関数を直接検証する。

func TestToOutboxMessage_OrderPlacedHasNoRoute(t *testing.T) {
	e := order.OrderPlaced{OrderID: "ORDER-1", ReservationRef: "RES-1", At: time.Now().UTC()}
	_, ok, err := toOutboxMessage(e, "trace-1")
	if err != nil {
		t.Fatalf("想定外のエラー: %v", err)
	}
	if ok {
		t.Fatalf("OrderPlaced はクロスコンテキストの送出経路を持たないはず（ok=true）")
	}
}

func TestToOutboxMessage_OrderCancelled(t *testing.T) {
	e := order.OrderCancelled{OrderID: "ORDER-1", ReservationRef: "RES-1", At: time.Now().UTC()}
	m, ok, err := toOutboxMessage(e, "trace-1")
	if err != nil {
		t.Fatalf("想定外のエラー: %v", err)
	}
	if !ok {
		t.Fatalf("OrderCancelled は送出経路を持つべき（ok=false）")
	}
	if m.Type != MessageTypeOrderCancelled {
		t.Fatalf("Type = %q, want %q", m.Type, MessageTypeOrderCancelled)
	}
	if m.TraceID != "trace-1" || m.ID == "" {
		t.Fatalf("メッセージのメタ情報が不正: %+v", m)
	}
	var p struct {
		ReservationRef string `json:"reservation_ref"`
		OrderID        string `json:"order_id"`
	}
	if err := json.Unmarshal(m.Payload, &p); err != nil {
		t.Fatalf("payload デコード失敗: %v", err)
	}
	if p.ReservationRef != "RES-1" || p.OrderID != "ORDER-1" {
		t.Fatalf("payload が不正: %+v", p)
	}
}

func TestConfirmReservationMessage(t *testing.T) {
	ref, err := order.NewReservationRef("REF-1")
	if err != nil {
		t.Fatalf("ReservationRef 生成失敗: %v", err)
	}
	m, err := confirmReservationMessage(ref, "trace-1")
	if err != nil {
		t.Fatalf("想定外のエラー: %v", err)
	}
	if m.Type != MessageTypeConfirmReservation {
		t.Fatalf("Type = %q, want %q", m.Type, MessageTypeConfirmReservation)
	}
	if m.TraceID != "trace-1" || m.ID == "" || m.OccurredAt.IsZero() {
		t.Fatalf("メッセージのメタ情報が不正: %+v", m)
	}
	var p struct {
		ReservationRef string `json:"reservation_ref"`
	}
	if err := json.Unmarshal(m.Payload, &p); err != nil {
		t.Fatalf("payload デコード失敗: %v", err)
	}
	if p.ReservationRef != "REF-1" {
		t.Fatalf("reservation_ref = %q, want REF-1", p.ReservationRef)
	}
}
