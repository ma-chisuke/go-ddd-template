package order_test

import (
	"errors"
	"testing"

	"github.com/example/go-ddd-template/contexts/ordering/internal/domain/order"
)

func TestReconstituteAndGetters(t *testing.T) {
	id := mustOrderID(t, "ORDER-9")
	cust := mustCustomerID(t, "CUST-9")
	lines := []order.OrderLine{mustLine(t, "SKU-A", 2, 100, "JPY")}
	total, err := order.NewMoney(200, "JPY")
	if err != nil {
		t.Fatalf("Money 生成失敗: %v", err)
	}
	ref, err := order.NewReservationRef("REF-9")
	if err != nil {
		t.Fatalf("ReservationRef 生成失敗: %v", err)
	}

	o := order.ReconstituteOrder(id, cust, lines, order.StatusCancelled, total, ref, 3)

	if o.ID().String() != "ORDER-9" {
		t.Fatalf("ID = %q", o.ID().String())
	}
	if o.CustomerID().String() != "CUST-9" {
		t.Fatalf("CustomerID = %q", o.CustomerID().String())
	}
	if o.Status() != order.StatusCancelled {
		t.Fatalf("Status = %v", o.Status())
	}
	if o.Version() != 3 {
		t.Fatalf("Version = %d", o.Version())
	}
	if o.ReservationRef().String() != "REF-9" {
		t.Fatalf("ReservationRef = %q", o.ReservationRef().String())
	}
	got := o.Lines()
	if len(got) != 1 {
		t.Fatalf("Lines 件数 = %d", len(got))
	}
	l := got[0]
	if l.SKU().String() != "SKU-A" || l.Quantity().Int() != 2 || l.UnitPrice().Amount() != 100 {
		t.Fatalf("明細が不正: %+v", l)
	}
	if l.Subtotal().Amount() != 200 {
		t.Fatalf("小計 = %d, want 200", l.Subtotal().Amount())
	}

	// 復元ではドメインイベントを発生させない。
	if events := o.PullEvents(); len(events) != 0 {
		t.Fatalf("復元でイベントが発生した: %d 件", len(events))
	}

	// MarkPersisted はバージョンを同期する。
	o.MarkPersisted(4)
	if o.Version() != 4 {
		t.Fatalf("MarkPersisted 後の Version = %d, want 4", o.Version())
	}
}

func TestEventOccurredAt(t *testing.T) {
	o, err := order.NewOrder(mustOrderID(t, "ORDER-1"), mustCustomerID(t, "CUST-1"),
		[]order.OrderLine{mustLine(t, "SKU-A", 1, 1000, "JPY")})
	if err != nil {
		t.Fatalf("注文作成失敗: %v", err)
	}
	placed := o.PullEvents()
	if len(placed) != 1 || placed[0].OccurredAt().IsZero() {
		t.Fatalf("OrderPlaced の OccurredAt が未設定: %+v", placed)
	}
	if err := o.Cancel(); err != nil {
		t.Fatalf("取消失敗: %v", err)
	}
	cancelled := o.PullEvents()
	if len(cancelled) != 1 || cancelled[0].OccurredAt().IsZero() {
		t.Fatalf("OrderCancelled の OccurredAt が未設定: %+v", cancelled)
	}
}

func TestStatusString(t *testing.T) {
	cases := map[order.Status]string{
		order.StatusConfirmed: "confirmed",
		order.StatusCancelled: "cancelled",
		order.Status(99):      "unknown",
	}
	for s, want := range cases {
		if got := s.String(); got != want {
			t.Fatalf("Status(%d).String() = %q, want %q", int(s), got, want)
		}
	}
}

func TestValueObjectZeroAndErrors(t *testing.T) {
	if !(order.OrderID{}).IsZero() {
		t.Fatalf("OrderID{} は IsZero であるべき")
	}
	if !(order.ReservationRef{}).IsZero() {
		t.Fatalf("ReservationRef{} は IsZero であるべき")
	}
	if !(order.Money{}).IsZero() {
		t.Fatalf("Money{} は IsZero であるべき")
	}
	if _, err := order.NewReservationRef("  "); !errors.Is(err, order.ErrInvalidReservationRef) {
		t.Fatalf("NewReservationRef 空 のエラー = %v, want ErrInvalidReservationRef", err)
	}
	if _, err := order.NewOrderID(""); !errors.Is(err, order.ErrInvalidOrderID) {
		t.Fatalf("NewOrderID 空 のエラー = %v, want ErrInvalidOrderID", err)
	}
	if _, err := order.NewCustomerID(""); !errors.Is(err, order.ErrInvalidCustomerID) {
		t.Fatalf("NewCustomerID 空 のエラー = %v, want ErrInvalidCustomerID", err)
	}
}
