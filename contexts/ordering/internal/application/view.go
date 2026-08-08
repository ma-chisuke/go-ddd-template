package application

import "github.com/example/go-ddd-template/contexts/ordering/internal/domain"

// OrderView は注文の読み取り用 DTO（プリミティブ値のみ）。境界（HTTP など）へ返すために
// ドメインの集約から射影する。ドメインの値オブジェクトはここには現れない。
type OrderView struct {
	ID             string
	CustomerID     string
	Status         string
	Lines          []OrderLineView
	TotalAmount    int64
	TotalCurrency  string
	ReservationRef string
	Version        int
}

// OrderLineView は注文明細の読み取り用 DTO。
type OrderLineView struct {
	SKU               string
	Quantity          int
	UnitPriceAmount   int64
	UnitPriceCurrency string
	SubtotalAmount    int64
	SubtotalCurrency  string
}

// toOrderView は注文集約を読み取り用 DTO へ射影する。
func toOrderView(o *domain.Order) OrderView {
	lines := make([]OrderLineView, 0, len(o.Lines()))
	for _, l := range o.Lines() {
		subtotal := l.Subtotal()
		lines = append(lines, OrderLineView{
			SKU:               l.SKU().String(),
			Quantity:          l.Quantity().Int(),
			UnitPriceAmount:   l.UnitPrice().Amount(),
			UnitPriceCurrency: l.UnitPrice().Currency(),
			SubtotalAmount:    subtotal.Amount(),
			SubtotalCurrency:  subtotal.Currency(),
		})
	}
	return OrderView{
		ID:             o.ID().String(),
		CustomerID:     o.CustomerID().String(),
		Status:         o.Status().String(),
		Lines:          lines,
		TotalAmount:    o.Total().Amount(),
		TotalCurrency:  o.Total().Currency(),
		ReservationRef: o.ReservationRef().String(),
		Version:        o.Version(),
	}
}

// ShipmentView は出荷の読み取り用 DTO（プリミティブ値のみ）。
//
// OrderID は注文の識別子であって注文の射影ではない。集約間は識別子で参照するという規則が、
// 読み取り用 DTO にもそのまま現れる（出荷の照会で注文の明細が返ることはない）。
type ShipmentView struct {
	ID      string
	OrderID string
	Status  string
	// TrackingNumber は preparing の出荷では空文字。
	TrackingNumber string
	Version        int
}

// toShipmentView は出荷集約を読み取り用 DTO へ射影する。
func toShipmentView(s *domain.Shipment) ShipmentView {
	return ShipmentView{
		ID:             s.ID().String(),
		OrderID:        s.OrderID().String(),
		Status:         s.Status().String(),
		TrackingNumber: s.TrackingNumber().String(),
		Version:        s.Version(),
	}
}
