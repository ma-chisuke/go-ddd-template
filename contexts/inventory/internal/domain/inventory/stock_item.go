package inventory

import (
	"fmt"
	"time"
)

// StockItem は在庫項目を表す集約ルート（aggregate root）。SKU ごとに 1 つ存在する。
//
// 純粋なドメインオブジェクトであり、context.Context・リポジトリ・永続化・IO・
// フレームワークのいずれにも依存しない。状態の変更はメソッドを通じてのみ行い、
// 不変条件（利用可能在庫は非負、1 参照につき有効な予約は高々 1 つ、など）を常に
// 自身で守る。
//
// 在庫の数え方（重要）:
//   - available … 「自由に予約できる」利用可能在庫。予約すると減り、解放すると戻る。
//   - reserved  … 有効な予約（pending + confirmed）の合計。状態ではなく導出値。
//   - 総在庫（on-hand）= available + reserved。
//
// version は楽観的排他制御のためのバージョン番号だが、集約はこれを「保持」するだけで、
// 比較（compare-and-set）はリポジトリが担う。集約自身がバージョンを増やすことはしない。
// 新規作成された集約の version は 0 であり、まだ永続化されていないことを表す。
// 永続化済みの集約は version >= 1 を持つ。
type StockItem struct {
	id           string
	sku          SKU
	available    Quantity
	reservations map[string]*Reservation // key: ReservationRef.String()。有効な予約のみを保持する。
	version      int
	events       []DomainEvent
}

// NewStockItem は新しい在庫項目を生成する。利用可能在庫 0、予約なし、version 0（未永続化）で始まる。
// id が空の場合は不正としてエラーを返す。
func NewStockItem(id string, sku SKU) (*StockItem, error) {
	if id == "" {
		return nil, fmt.Errorf("在庫項目の id は空にできません: %w", ErrInvalidSKU)
	}
	return &StockItem{
		id:           id,
		sku:          sku,
		available:    Quantity{}, // 数量 0
		reservations: make(map[string]*Reservation),
		version:      0,
	}, nil
}

// ReconstituteStockItem は永続化された状態から集約を復元する。
// リポジトリ（送信アダプタ）が保存済みの行から集約を再構築する際に用いる。
// すでに検証済みの状態を組み立て直すだけなので、ドメインイベントは発生させない。
// reservations は復元対象の有効な予約（pending / confirmed）の一覧。
func ReconstituteStockItem(id string, sku SKU, available Quantity, version int, reservations []*Reservation) *StockItem {
	m := make(map[string]*Reservation, len(reservations))
	for _, r := range reservations {
		m[r.ref.String()] = r
	}
	return &StockItem{
		id:           id,
		sku:          sku,
		available:    available,
		reservations: m,
		version:      version,
	}
}

// Replenish は在庫を補充する。補充数量 0 は無意味な操作なので ErrInvalidQuantity を返す。
// 成功した場合は利用可能在庫を増やし、StockReplenished イベントを記録する。
func (s *StockItem) Replenish(qty Quantity) error {
	if qty.IsZero() {
		return fmt.Errorf("補充数量は 1 以上でなければなりません: %w", ErrInvalidQuantity)
	}
	s.available = s.available.Add(qty)
	s.recordEvent(StockReplenished{
		SKU:           s.sku.String(),
		QuantityAdded: qty.Int(),
		Available:     s.available.Int(),
		At:            time.Now().UTC(),
	})
	return nil
}

// Reserve は指定参照 ref に対して数量 qty の仮予約（pending）を作る。TTL を付け、
// 期限切れになると Reaper が解放する（二相予約の第 1 相）。
//
// 規則:
//   - 要求数量は 1 以上（0 は ErrInvalidQuantity）。
//   - ref に既に有効な予約があれば「冪等な no-op」で成功を返す（二重予約しない）。
//     自動リトライや呼び出し側の再送のもとで安全にするため。
//   - 予約が許容されるのは要求数量 <= available のときのみ。上回れば ErrInsufficientStock。
//
// 成功時は available を要求分だけ減らし、pending 予約を追加して StockReserved を記録する。
// available が 0 に到達したら StockDepleted も記録する（発行＋ログのみで購読者はいない）。
func (s *StockItem) Reserve(ref ReservationRef, qty Quantity, ttl time.Duration) error {
	if ref.IsZero() {
		return fmt.Errorf("予約参照は空にできません: %w", ErrInvalidReservationRef)
	}
	if qty.IsZero() {
		return fmt.Errorf("予約数量は 1 以上でなければなりません: %w", ErrInvalidQuantity)
	}
	// 冪等性: 既に有効な予約があれば no-op。
	if _, ok := s.reservations[ref.String()]; ok {
		return nil
	}
	if qty.GreaterThan(s.available) {
		return fmt.Errorf("SKU %q: 要求 %d > 利用可能 %d: %w", s.sku.String(), qty.Int(), s.available.Int(), ErrInsufficientStock)
	}
	remaining, err := s.available.Sub(qty)
	if err != nil {
		// 直前に qty <= available を確認済みなので通常到達しない。防御的に扱う。
		return err
	}
	s.available = remaining
	s.reservations[ref.String()] = &Reservation{
		ref:       ref,
		qty:       qty,
		status:    ReservationPending,
		expiresAt: time.Now().UTC().Add(ttl),
	}
	s.recordEvent(StockReserved{
		SKU:            s.sku.String(),
		ReservationRef: ref.String(),
		Quantity:       qty.Int(),
		Available:      s.available.Int(),
		At:             time.Now().UTC(),
	})
	if s.available.IsZero() {
		s.recordEvent(StockDepleted{
			SKU: s.sku.String(),
			At:  time.Now().UTC(),
		})
	}
	return nil
}

// Confirm は参照 ref の予約を pending から confirmed へ遷移させ、TTL をクリアする
// （二相予約の第 2 相）。以後 Reaper はこの予約を解放しない。
//
// 規則:
//   - 有効な予約が無い ref（既に Reap 済み、または速い取消で解放済み）は ErrReservationNotFound。
//   - 既に confirmed の ref は「冪等な no-op」で成功を返す。
//
// confirmed 化は available を変えない（在庫は引き続き予約として押さえられたまま）。
func (s *StockItem) Confirm(ref ReservationRef) error {
	r, ok := s.reservations[ref.String()]
	if !ok {
		return fmt.Errorf("SKU %q 参照 %q: %w", s.sku.String(), ref.String(), ErrReservationNotFound)
	}
	if r.status == ReservationConfirmed {
		return nil // 冪等 no-op
	}
	r.status = ReservationConfirmed
	r.expiresAt = time.Time{} // TTL をクリア
	s.recordEvent(StockReservationConfirmed{
		SKU:            s.sku.String(),
		ReservationRef: ref.String(),
		At:             time.Now().UTC(),
	})
	return nil
}

// Release は参照 ref の予約（pending / confirmed いずれも）を解放し、数量を available へ
// 戻す。未知・解放済みの ref に対しては「冪等な no-op」で成功を返す。
func (s *StockItem) Release(ref ReservationRef) error {
	r, ok := s.reservations[ref.String()]
	if !ok {
		return nil // 冪等 no-op（未知 / 解放済み）
	}
	s.available = s.available.Add(r.qty)
	delete(s.reservations, ref.String())
	s.recordEvent(StockReleased{
		SKU:            s.sku.String(),
		ReservationRef: ref.String(),
		Quantity:       r.qty.Int(),
		Available:      s.available.Int(),
		At:             time.Now().UTC(),
	})
	return nil
}

// ReapExpired は now 時点で期限切れの pending 予約「のみ」を解放し、その StockReleased
// イベント一覧を返す。confirmed 予約は決して解放しない。
//
// 「reserve は commit したが、確定（Order の durable 化）に至らなかった」孤児 pending を、
// 生きている confirmed 予約の在庫を巻き込まずに healing するための処理。
//
// 注意（他メソッドとの違い）: このメソッドは一括処理のため、発生したイベントを内部に
// 蓄積（recordEvent）せず、戻り値として直接返す。呼び出し側（Reaper ユースケース）は
// 戻り値のイベントをそのまま配信する。
func (s *StockItem) ReapExpired(now time.Time) []DomainEvent {
	var events []DomainEvent
	for key, r := range s.reservations {
		if r.status != ReservationPending || !r.isExpired(now) {
			continue
		}
		s.available = s.available.Add(r.qty)
		delete(s.reservations, key)
		events = append(events, StockReleased{
			SKU:            s.sku.String(),
			ReservationRef: r.ref.String(),
			Quantity:       r.qty.Int(),
			Available:      s.available.Int(),
			At:             now.UTC(),
		})
	}
	return events
}

// Available は現在の利用可能（自由に予約できる）在庫数を返す。
func (s *StockItem) Available() Quantity {
	return s.available
}

// Reserved は引当済み（予約済み）の在庫数を返す。これは状態ではなく、有効な予約
// （pending + confirmed）の数量合計として導出される値である。
func (s *StockItem) Reserved() Quantity {
	total := Quantity{}
	for _, r := range s.reservations {
		total = total.Add(r.qty)
	}
	return total
}

// Reservations は集約が保持する有効な予約の一覧を返す（永続化アダプタが状態を書き出す用）。
// 返すのはコピーであり、これを変更しても集約の状態には影響しない。
func (s *StockItem) Reservations() []*Reservation {
	out := make([]*Reservation, 0, len(s.reservations))
	for _, r := range s.reservations {
		out = append(out, r)
	}
	return out
}

// ID は集約の識別子を返す。
func (s *StockItem) ID() string {
	return s.id
}

// SKU は在庫識別子を返す。
func (s *StockItem) SKU() SKU {
	return s.sku
}

// Version は集約が保持しているバージョン番号を返す。
// リポジトリはこの値を「期待バージョン」として楽観的排他制御の比較に用いる。
func (s *StockItem) Version() int {
	return s.version
}

// MarkPersisted は永続化アダプタ（リポジトリ）が書き込み成功後に呼び出し、
// 集約が保持するバージョンを新しい値へ同期する。楽観的排他制御の比較はリポジトリが行い、
// その結果としての新バージョンをこのメソッドで集約へ反映する。
// アプリケーション層やドメインサービスから呼び出してはならない（リポジトリとの契約）。
func (s *StockItem) MarkPersisted(version int) {
	s.version = version
}

// PullEvents は蓄積されたドメインイベントを返し、集約内部のイベントを空にする。
// アプリケーション層はこれを取り出し、永続化の成功後にディスパッチする。
// （ReapExpired の解放イベントはここには含まれない。ReapExpired の戻り値を使うこと。）
func (s *StockItem) PullEvents() []DomainEvent {
	events := s.events
	s.events = nil
	return events
}

// hasReservation は ref に対する有効な予約を保持しているかどうかを返す。
// 同一パッケージの ReservationService がマルチ SKU の事前検証で用いる。
func (s *StockItem) hasReservation(ref ReservationRef) bool {
	_, ok := s.reservations[ref.String()]
	return ok
}

// recordEvent はドメインイベントを内部に蓄積する。
func (s *StockItem) recordEvent(e DomainEvent) {
	s.events = append(s.events, e)
}
