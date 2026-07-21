package order

import "time"

// Order は注文を表す集約ルート（aggregate root）。
//
// 純粋なドメインオブジェクトであり、context.Context・リポジトリ・永続化・IO・
// フレームワークのいずれにも依存しない。状態の変更はメソッドを通じてのみ行い、
// 不変条件（明細は 1 行以上、合計は非負、取消は Confirmed からのみ）を常に自身で守る。
//
// 状態モデル: Confirmed -> Cancelled のみ。作成（place）時に在庫予約が成立すると
// Confirmed で始まり、Cancel() で Cancelled へ遷移する。
//
// version は楽観的排他制御のためのバージョン番号だが、集約はこれを「保持」するだけで、
// 比較（compare-and-set）はリポジトリが担う。新規作成された集約の version は 0 であり、
// まだ永続化されていないことを表す。永続化済みの集約は version >= 1 を持つ。
//
// reservationRef は作成時に導出して捕捉する在庫予約の相関 ID。取消時の（在庫側の）
// 解放を駆動するために保持する。
type Order struct {
	id             OrderID
	customer       CustomerID
	lines          []OrderLine
	status         Status
	total          Money
	reservationRef ReservationRef
	version        int
	events         []DomainEvent
}

// NewOrder は明細から新しい注文を作成する。
//
// 規則:
//   - 明細は 1 行以上（空なら ErrEmptyOrder）。
//   - 合計は各行の小計の総和。行間で通貨が食い違えば ErrInvalidMoney（Money.Add による）。
//
// 成功すると Confirmed 状態で始まり、注文 ID から予約参照を決定的に導出して捕捉し、
// OrderPlaced イベントを記録する。version は 0（未永続化）。
func NewOrder(id OrderID, customer CustomerID, lines []OrderLine) (*Order, error) {
	if len(lines) == 0 {
		return nil, ErrEmptyOrder
	}

	total := Money{}
	for _, l := range lines {
		next, err := total.Add(l.Subtotal())
		if err != nil {
			return nil, err
		}
		total = next
	}

	ref := DeriveReservationRef(id)
	o := &Order{
		id:             id,
		customer:       customer,
		lines:          lines,
		status:         StatusConfirmed,
		total:          total,
		reservationRef: ref,
		version:        0,
	}
	o.recordEvent(OrderPlaced{
		OrderID:        id.String(),
		CustomerID:     customer.String(),
		ReservationRef: ref.String(),
		TotalAmount:    total.Amount(),
		Currency:       total.Currency(),
		At:             time.Now().UTC(),
	})
	return o, nil
}

// ReconstituteOrder は永続化された状態から集約を復元する。
// リポジトリ（送信アダプタ）が保存済みの行から集約を再構築する際に用いる。
// すでに検証済みの状態を組み立て直すだけなので、ドメインイベントは発生させない。
func ReconstituteOrder(id OrderID, customer CustomerID, lines []OrderLine, status Status, total Money, ref ReservationRef, version int) *Order {
	return &Order{
		id:             id,
		customer:       customer,
		lines:          lines,
		status:         status,
		total:          total,
		reservationRef: ref,
		version:        version,
	}
}

// Cancel は注文を取り消す。取消が許容されるのは Confirmed 状態の注文のみで、
// それ以外は ErrOrderNotConfirmed を返す。成功すると Cancelled へ遷移し、
// OrderCancelled イベントを記録する（在庫解放は在庫側が非同期に購読して行う）。
func (o *Order) Cancel() error {
	if o.status != StatusConfirmed {
		return ErrOrderNotConfirmed
	}
	o.status = StatusCancelled
	o.recordEvent(OrderCancelled{
		OrderID:        o.id.String(),
		ReservationRef: o.reservationRef.String(),
		At:             time.Now().UTC(),
	})
	return nil
}

// ID は注文の識別子を返す。
func (o *Order) ID() OrderID {
	return o.id
}

// CustomerID は注文者（顧客）の識別子を返す。
func (o *Order) CustomerID() CustomerID {
	return o.customer
}

// Lines は注文明細の一覧を返す（永続化アダプタや表示のための読み取り用）。
// 返すのはコピーであり、これを変更しても集約の状態には影響しない。
func (o *Order) Lines() []OrderLine {
	out := make([]OrderLine, len(o.lines))
	copy(out, o.lines)
	return out
}

// Total は注文合計金額を返す（各行の小計の総和で >= 0）。
func (o *Order) Total() Money {
	return o.total
}

// Status は注文状態を返す。
func (o *Order) Status() Status {
	return o.status
}

// ReservationRef は在庫予約に用いる予約参照を返す。
func (o *Order) ReservationRef() ReservationRef {
	return o.reservationRef
}

// Version は集約が保持しているバージョン番号を返す。
// リポジトリはこの値を「期待バージョン」として楽観的排他制御の比較に用いる。
func (o *Order) Version() int {
	return o.version
}

// MarkPersisted は永続化アダプタ（リポジトリ）が書き込み成功後に呼び出し、
// 集約が保持するバージョンを新しい値へ同期する。楽観的排他制御の比較はリポジトリが行い、
// その結果としての新バージョンをこのメソッドで集約へ反映する。
// アプリケーション層やドメインサービスから呼び出してはならない（リポジトリとの契約）。
func (o *Order) MarkPersisted(version int) {
	o.version = version
}

// PullEvents は蓄積されたドメインイベントを返し、集約内部のイベントを空にする。
// アプリケーション層はこれを取り出し、クロスコンテキストイベントは翻訳してアウトボックスへ、
// プロセス内イベントはディスパッチャへ振り分ける。
func (o *Order) PullEvents() []DomainEvent {
	events := o.events
	o.events = nil
	return events
}

// recordEvent はドメインイベントを内部に蓄積する。
func (o *Order) recordEvent(e DomainEvent) {
	o.events = append(o.events, e)
}
