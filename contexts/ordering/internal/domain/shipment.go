// 出荷の集約ルートと、その状態・識別子・追跡番号。
//
// 束ね方の根拠は CONVENTIONS.md の B-3 則 1（型はそれが属する概念のファイルに置く）である。
// ShipmentID・TrackingNumber・ShipmentStatus がここに在るのは「文字列を包む識別子」という
// 技術的な種類のためではなく、いずれも出荷という概念の一部だからである。技術的な種類で
// 束ねた identifiers.go のような容れ物は作らない（B-4）。

package domain

import (
	"strings"
	"time"
)

// Shipment は出荷を表す集約ルート（aggregate root）。
//
// 注文（Order）とは別のライフサイクルを持ち、別のトランザクション境界に属する。
// **注文は識別子で参照し、*Order を保持しない** — これが DDD の「集約間は識別子で参照する」
// であり、このテンプレートで唯一その形を実演している箇所である。
//
// 状態モデル: preparing -> shipped のみ。NewShipment は必ず preparing で始まり、
// MarkShipped で shipped へ遷移する。shipped は終端で、そこからの遷移は無い。
//
// version は楽観的排他制御のためのバージョン番号だが、集約はこれを「保持」するだけで、
// 比較（compare-and-set）はリポジトリが担う。新規作成された集約の version は 0 であり、
// まだ永続化されていないことを表す。永続化済みの集約は version >= 1 を持つ。
type Shipment struct {
	id       ShipmentID
	orderID  OrderID // 別の集約ルートへの参照。識別子のみを保持する。
	status   ShipmentStatus
	tracking TrackingNumber // preparing の間はゼロ値。
	version  int
	events   []DomainEvent
}

// コンパイル時に集約ルートの契約を満たしていることを確認する。
// 機械検査（12 / 13 / 14）はこの表明から集約ルートの集合を得る。
var _ AggregateRoot = (*Shipment)(nil)

// NewShipment は指定した注文に対する出荷を新規作成する。
//
// 規則:
//   - 出荷 ID は空にできない（空なら ErrInvalidShipmentID）。
//   - 注文 ID は空にできない（空なら ErrInvalidOrderID）。
//
// 成功すると preparing 状態・追跡番号ゼロ値・version 0（未永続化）で始まる。
//
// **「注文が出荷可能な状態か」はここでは判定しない。** それは 2 つの集約をまたぐ条件であり、
// Shipment 集約の不変条件ではないからである。事前条件としてユースケースが課す
// （application.ErrOrderNotConfirmedForShipment）。集約は Order の状態を知らないし、
// 知る手段も持たない（ドメイン層はリポジトリを import しない）。
func NewShipment(id ShipmentID, orderID OrderID) (*Shipment, error) {
	if id.IsZero() {
		return nil, VShipmentID.Violated("出荷 ID は空にできません")
	}
	if orderID.IsZero() {
		return nil, VOrderID.Violated("注文 ID は空にできません")
	}
	return &Shipment{
		id:      id,
		orderID: orderID,
		status:  StatusPreparing,
		version: 0,
	}, nil
}

// ReconstituteShipment は永続化された状態から集約を復元する。
// リポジトリ（送信アダプタ）が保存済みの行から集約を再構築する際に用いる。
// すでに検証済みの状態を組み立て直すだけなので、ドメインイベントは発生させない。
func ReconstituteShipment(id ShipmentID, orderID OrderID, status ShipmentStatus, tracking TrackingNumber, version int) *Shipment {
	return &Shipment{
		id:       id,
		orderID:  orderID,
		status:   status,
		tracking: tracking,
		version:  version,
	}
}

// MarkShipped は出荷を発送済みにする。遷移できるのは preparing 状態の出荷のみで、
// それ以外は ErrShipmentNotPreparing を返し、状態を変更しない。
// 成功すると shipped へ遷移し、ShipmentDispatched イベントを記録する。
//
// **冪等にしない。** 既に shipped の出荷に対する再呼び出しはエラーである。
// StockItem.Release が冪等 no-op なのと対照的だが根拠がある — Release は「解放されている」
// という結果が目的なので再実行が無害である一方、MarkShipped は追跡番号という新しい情報を
// 伴う状態変更であり、2 度目の呼び出しは「別の追跡番号で上書きしようとしている」可能性が
// ある。黙って成功させるべきではない。
func (s *Shipment) MarkShipped(tracking TrackingNumber) error {
	if s.status != StatusPreparing {
		return ErrShipmentNotPreparing
	}
	if tracking.IsZero() {
		return VTrackingNumber.Violated("追跡番号は空にできません")
	}
	s.status = StatusShipped
	s.tracking = tracking
	s.recordEvent(ShipmentDispatched{
		ShipmentID:     s.id.String(),
		OrderID:        s.orderID.String(),
		TrackingNumber: tracking.String(),
		At:             time.Now().UTC(),
	})
	return nil
}

// ID は出荷の識別子を返す。
func (s *Shipment) ID() ShipmentID {
	return s.id
}

// OrderID は出荷が参照する注文の識別子を返す（別の集約ルートへの参照）。
func (s *Shipment) OrderID() OrderID {
	return s.orderID
}

// Status は出荷状態を返す。
func (s *Shipment) Status() ShipmentStatus {
	return s.status
}

// TrackingNumber は配送業者の追跡番号を返す（preparing の間はゼロ値）。
func (s *Shipment) TrackingNumber() TrackingNumber {
	return s.tracking
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
// ShipmentDispatched はクロスコンテキストへの送信を伴わないプロセス内イベントなので、
// アプリケーション層はこれを EventDispatcher へ配信する（アウトボックスへは積まない）。
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
// 配送完了（delivered）や出荷取消は範囲外である。
type ShipmentStatus int

const (
	// StatusPreparing は準備中の出荷。NewShipment はこの状態で始まる。
	StatusPreparing ShipmentStatus = iota
	// StatusShipped は発送済みの出荷。preparing からのみ遷移でき、追跡番号を伴う。
	StatusShipped
)

// String は状態の文字列表現を返す（永続化・ログ・API 表示用）。
func (s ShipmentStatus) String() string {
	switch s {
	case StatusPreparing:
		return "preparing"
	case StatusShipped:
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
func (i ShipmentID) String() string {
	return i.value
}

// IsZero はゼロ値（未設定）かどうかを返す。
func (i ShipmentID) IsZero() bool {
	return i.value == ""
}

// TrackingNumber は配送業者の追跡番号を表す値オブジェクト。不変であり、生成後は
// 値を変更できない。空文字は許容しない。
//
// 書式は配送業者ごとに異なるため、このテンプレートは「空でない」以上の制約を課さない。
// 採用者が業者を確定させたら、ここに書式の検証を足す。
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

// String は追跡番号の文字列表現を返す。
func (t TrackingNumber) String() string {
	return t.value
}

// IsZero はゼロ値（未設定）かどうかを返す。
func (t TrackingNumber) IsZero() bool {
	return t.value == ""
}
