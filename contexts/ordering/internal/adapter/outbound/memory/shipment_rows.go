package memory

import (
	"fmt"

	"github.com/example/go-ddd-template/contexts/ordering/internal/domain"
)

// shipmentRow は確定済み（コミット済み）の出荷行。
//
// 注文への参照は order_id 相当の文字列 1 つだけである。集約間は識別子で参照するため、
// 出荷行に注文の明細や状態を持たせない。
type shipmentRow struct {
	id             string
	orderID        string
	status         string
	trackingNumber string
	version        int
}

// ShipmentRows は出荷集約の確定済み行（key: ShipmentID 文字列）を保持するインメモリの
// backing store。共通機構 rows[R]（rows.go）を shipmentRow で特殊化したものである。
//
// OrderRows と同じく型エイリアス（= ）で作る。集約を 1 つ足しても rows.go には差分が
// 出ない — それがこの機構の目的である。
type ShipmentRows = rows[shipmentRow]

// NewShipmentRows は空の出荷行 backing store を生成する。
func NewShipmentRows() *ShipmentRows {
	return &rows[shipmentRow]{m: make(map[string]shipmentRow)}
}

// parseShipmentStatus は永続化された文字列を出荷状態へ変換する。
func parseShipmentStatus(s string) (domain.ShipmentStatus, error) {
	switch s {
	case "preparing":
		return domain.ShipmentPreparing, nil
	case "shipped":
		return domain.ShipmentShipped, nil
	default:
		return domain.ShipmentPreparing, fmt.Errorf("永続化された出荷状態が不正です: %q", s)
	}
}

// shipmentRowToShipment は確定済みの行から集約を復元する。
func shipmentRowToShipment(r shipmentRow) (*domain.Shipment, error) {
	shipmentID, err := domain.NewShipmentID(r.id)
	if err != nil {
		return nil, fmt.Errorf("永続化された出荷 ID が不正です: %w", err)
	}
	orderID, err := domain.NewOrderID(r.orderID)
	if err != nil {
		return nil, fmt.Errorf("永続化された注文 ID が不正です: %w", err)
	}
	status, err := parseShipmentStatus(r.status)
	if err != nil {
		return nil, err
	}
	// preparing の出荷は追跡番号を持たない。空文字はゼロ値として復元する
	// （NewTrackingNumber は空文字を弾くので、ここを通してはならない）。
	var tn domain.TrackingNumber
	if r.trackingNumber != "" {
		tn, err = domain.NewTrackingNumber(r.trackingNumber)
		if err != nil {
			return nil, fmt.Errorf("永続化された追跡番号が不正です: %w", err)
		}
	}
	return domain.ReconstituteShipment(shipmentID, orderID, status, tn, r.version), nil
}

// shipmentToShipmentRow は集約を、指定バージョンで確定行へ変換する。
func shipmentToShipmentRow(s *domain.Shipment, version int) shipmentRow {
	return shipmentRow{
		id:             s.ID().String(),
		orderID:        s.OrderID().String(),
		status:         s.Status().String(),
		trackingNumber: s.TrackingNumber().String(),
		version:        version,
	}
}
