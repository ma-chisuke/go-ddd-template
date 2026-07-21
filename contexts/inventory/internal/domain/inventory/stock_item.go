package inventory

import (
	"fmt"
	"time"
)

// StockItem は在庫項目を表す集約ルート（aggregate root）。
//
// 純粋なドメインオブジェクトであり、context.Context・リポジトリ・永続化・IO・
// フレームワークのいずれにも依存しない。状態の変更はメソッドを通じてのみ行い、
// 不変条件（利用可能在庫は非負、など）を常に自身で守る。
//
// version は楽観的排他制御のためのバージョン番号だが、集約はこれを「保持」するだけで、
// 比較（compare-and-set）はリポジトリが担う。集約自身がバージョンを増やすことはしない。
// 新規作成された集約の version は 0 であり、まだ永続化されていないことを表す。
// 永続化済みの集約は version >= 1 を持つ。
type StockItem struct {
	id        string
	sku       SKU
	available Quantity
	version   int
	events    []DomainEvent
}

// NewStockItem は新しい在庫項目を生成する。利用可能在庫 0、version 0（未永続化）で始まる。
// id が空の場合は不正としてエラーを返す。
func NewStockItem(id string, sku SKU) (*StockItem, error) {
	if id == "" {
		return nil, fmt.Errorf("在庫項目の id は空にできません: %w", ErrInvalidSKU)
	}
	return &StockItem{
		id:        id,
		sku:       sku,
		available: Quantity{}, // 数量 0
		version:   0,
	}, nil
}

// ReconstituteStockItem は永続化された状態から集約を復元する。
// リポジトリ（永続化アダプタ）が保存済みの行から集約を再構築する際に用いる。
// すでに検証済みの状態を組み立て直すだけなので、ドメインイベントは発生させない。
func ReconstituteStockItem(id string, sku SKU, available Quantity, version int) *StockItem {
	return &StockItem{
		id:        id,
		sku:       sku,
		available: available,
		version:   version,
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

// Available は現在の利用可能在庫数を返す。
func (s *StockItem) Available() Quantity {
	return s.available
}

// Reserved は引当済み（予約済み）の在庫数を返す。
// このスライスでは引当機能が未実装のため、常に 0 を返す。
// （引当・予約の導入は後続の作業で行う。）
func (s *StockItem) Reserved() Quantity {
	return Quantity{} // 数量 0
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
func (s *StockItem) PullEvents() []DomainEvent {
	events := s.events
	s.events = nil
	return events
}

// recordEvent はドメインイベントを内部に蓄積する。
func (s *StockItem) recordEvent(e DomainEvent) {
	s.events = append(s.events, e)
}
