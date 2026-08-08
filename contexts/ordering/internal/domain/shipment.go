// 出荷の集約ルートと、その状態・識別子・追跡番号。
//
// 束ね方の根拠は CONVENTIONS.md の B-3 則 1（型はそれが属する概念のファイルに置く）と
// 則 2（小さな型はそれを最も使う型のファイルに同居させる）である。ShipmentID・
// ShipmentStatus・TrackingNumber がここに在るのは「文字列を包む識別子」という技術的な
// 種類のためではなく、いずれも出荷という概念の一部だからである。Order が order.go に
// OrderID / CustomerID / ReservationRef / OrderStatus を同居させているのと同じ形である。
//
// 値オブジェクトは境界づけられたコンテキストごとに独立して所有する。他コンテキストへは
// これらの内部型をそのまま渡さず、境界で文字列などの翻訳済み表現へ変換する。

package domain

import (
	"strings"
	"time"
)

// Shipment は出荷を表す集約ルート（aggregate root）。Order とは別のライフサイクルを持つ。
//
// Order を「識別子で」参照し、実体（*Order）は保持しない — これがテンプレートが示す
// 「集約間は識別子で参照する」という DDD 規則の実演である。出荷の整合性境界は出荷自身で
// 閉じており、注文の状態は出荷の不変条件に含まれない。「注文が確定済みか」は出荷を準備する
// ときの**事前条件**であって不変条件ではないため、その検査のために 2 つの集約を同一
// トランザクションへ巻き込まない（アプリケーション層がトランザクションの外で確かめる）。
//
// 純粋なドメインオブジェクトであり、context.Context・リポジトリ・永続化・IO・
// フレームワークのいずれにも依存しない。
//
// 状態モデル: preparing -> shipped のみ。追跡番号は shipped のときだけ意味を持つ。
//
// version は楽観的排他制御のためのバージョン番号だが、集約はこれを「保持」するだけで、
// 比較（compare-and-set）はリポジトリが担う。新規作成された集約の version は 0 であり、
// まだ永続化されていないことを表す。永続化済みの集約は version >= 1 を持つ。
type Shipment struct {
	id             ShipmentID
	orderID        OrderID // 識別子による参照。*Order ではない
	status         ShipmentStatus
	trackingNumber TrackingNumber // preparing の間はゼロ値
	version        int
	events         []DomainEvent
}

// NewShipment は新しい出荷を準備中（preparing）で作成する。version は 0（未永続化）で、
// ドメインイベントは記録しない（発送は MarkShipped が担う）。
//
// error を返さないのは、引数がいずれも検証済みの値オブジェクトであり、失敗経路が
// 存在しないためである。到達不能な分岐を作らない（同じ理由で NewOrderLine も
// error を返さない）。
func NewShipment(id ShipmentID, orderID OrderID) *Shipment {
	return &Shipment{
		id:      id,
		orderID: orderID,
		status:  ShipmentPreparing,
		version: 0,
	}
}

// ReconstituteShipment は永続化された状態から集約を復元する。
// リポジトリ（送信アダプタ）が保存済みの行から集約を再構築する際に用いる。
// すでに検証済みの状態を組み立て直すだけなので、ドメインイベントは発生させない。
func ReconstituteShipment(id ShipmentID, orderID OrderID, status ShipmentStatus, tn TrackingNumber, version int) *Shipment {
	return &Shipment{
		id:             id,
		orderID:        orderID,
		status:         status,
		trackingNumber: tn,
		version:        version,
	}
}

// MarkShipped は出荷を発送済みへ遷移させ、追跡番号を確定する。
//
// 遷移が許容されるのは preparing 状態の出荷のみで、それ以外は ErrShipmentNotPreparing を
// 返す（再発送は無い）。成功すると ShipmentDispatched を記録する。
func (s *Shipment) MarkShipped(tn TrackingNumber) error {
	if s.status != ShipmentPreparing {
		return ErrShipmentNotPreparing
	}
	s.status = ShipmentShipped
	s.trackingNumber = tn
	s.recordEvent(ShipmentDispatched{
		ShipmentID:     s.id.String(),
		OrderID:        s.orderID.String(),
		TrackingNumber: tn.String(),
		At:             time.Now().UTC(),
	})
	return nil
}

// ID は出荷の識別子を返す。
func (s *Shipment) ID() ShipmentID {
	return s.id
}

// OrderID は出荷対象の注文の識別子を返す。返すのは識別子であって注文の実体ではない。
func (s *Shipment) OrderID() OrderID {
	return s.orderID
}

// Status は出荷状態を返す。
func (s *Shipment) Status() ShipmentStatus {
	return s.status
}

// TrackingNumber は追跡番号を返す（preparing の間はゼロ値）。
func (s *Shipment) TrackingNumber() TrackingNumber {
	return s.trackingNumber
}

// Version は集約が保持しているバージョン番号を返す。
// リポジトリはこの値を「期待バージョン」として楽観的排他制御の比較に用いる。
func (s *Shipment) Version() int {
	return s.version
}

// MarkPersisted は永続化アダプタ（リポジトリ）が書き込み成功後に呼び出し、
// 集約が保持するバージョンを新しい値へ同期する。
// アプリケーション層やドメインサービスから呼び出してはならない（リポジトリとの契約）。
func (s *Shipment) MarkPersisted(version int) {
	s.version = version
}

// PullEvents は蓄積されたドメインイベントを返し、集約内部のイベントを空にする。
// アプリケーション層はこれを取り出し、永続化の成功後に配信する。
func (s *Shipment) PullEvents() []DomainEvent {
	events := s.events
	s.events = nil
	return events
}

// recordEvent はドメインイベントを内部に蓄積する。
func (s *Shipment) recordEvent(e DomainEvent) {
	s.events = append(s.events, e)
}

// ShipmentStatus は出荷の状態を表す。v1 の状態モデルは preparing -> shipped のみで、
// 配達完了（delivered）や返送は範囲外のため状態を持たない。
type ShipmentStatus int

const (
	// ShipmentPreparing は準備中の出荷。作成時にこの状態で始まる。
	ShipmentPreparing ShipmentStatus = iota
	// ShipmentShipped は発送済みの出荷。preparing からのみ遷移できる。
	ShipmentShipped
)

// String は状態の文字列表現を返す（永続化・ログ・API 表示用）。
func (s ShipmentStatus) String() string {
	switch s {
	case ShipmentPreparing:
		return "preparing"
	case ShipmentShipped:
		return "shipped"
	default:
		return "unknown"
	}
}

// ShipmentID は出荷を識別する値オブジェクト。不変であり、生成後は値を変更できない。
// 空文字は許容しない。採番そのもの（ID 生成）はアプリケーション層が行い、ドメインは
// 与えられた文字列を検証して包むだけである。
type ShipmentID struct {
	value string
}

// NewShipmentID は出荷 ID を検証して生成する。前後の空白を取り除いた結果が空なら
// ErrInvalidShipmentID を包んだ FieldViolation を返す。
func NewShipmentID(s string) (ShipmentID, error) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return ShipmentID{}, VShipmentID.Violated("出荷 ID は空にできません")
	}
	return ShipmentID{value: trimmed}, nil
}

// String は出荷 ID の文字列表現を返す。
func (s ShipmentID) String() string {
	return s.value
}

// IsZero はゼロ値（未設定）かどうかを返す。
func (s ShipmentID) IsZero() bool {
	return s.value == ""
}

// TrackingNumber は配送業者の追跡番号を表す値オブジェクト。不変であり、生成後は値を
// 変更できない。空文字は許容しない。
//
// 意味を持つのは出荷が shipped のときだけである。preparing の間はゼロ値であり、
// 永続化では空文字として表現する。
type TrackingNumber struct {
	value string
}

// NewTrackingNumber は追跡番号を検証して生成する。前後の空白を取り除いた結果が空なら
// ErrInvalidTrackingNumber を包んだ FieldViolation を返す。
func NewTrackingNumber(s string) (TrackingNumber, error) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return TrackingNumber{}, VTrackingNumber.Violated("追跡番号は空にできません")
	}
	return TrackingNumber{value: trimmed}, nil
}

// String は追跡番号の文字列表現を返す（preparing の出荷では空文字）。
func (t TrackingNumber) String() string {
	return t.value
}

// IsZero はゼロ値（未設定）かどうかを返す。
func (t TrackingNumber) IsZero() bool {
	return t.value == ""
}
