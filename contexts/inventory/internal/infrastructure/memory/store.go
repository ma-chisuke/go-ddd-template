// Package memory はインメモリのインフラストラクチャ層アダプタを提供する。
//
// これはテスト用のモックではなく、application 層のポート（StockStore、UnitOfWork）を
// きちんと実装した「本物のアダプタ」である。擬似トランザクションと楽観的排他制御の
// 版チェックを備えており、DB を用意しなくても ErrConcurrencyConflict を再現できる。
// ドメイン層とアプリケーション層を DB 非依存で高速にテストするために使う。
package memory

import (
	"fmt"
	"sync"

	"github.com/example/go-ddd-template/contexts/inventory/internal/domain/inventory"
)

// record は確定済み（コミット済み）の在庫行。
type record struct {
	id        string
	sku       string
	available int
	version   int
}

// Store はインメモリの確定済みデータを保持する。並行アクセスを mutex で守る。
type Store struct {
	mu   sync.Mutex
	rows map[string]record // key: SKU 文字列
}

// NewStore は空の在庫ストアを生成する。
func NewStore() *Store {
	return &Store{rows: make(map[string]record)}
}

// load は確定済みデータから在庫項目を読み込み、集約を復元する。
// リポジトリの Load 経由でのみ呼ばれる。
func (s *Store) load(sku inventory.SKU) (*inventory.StockItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	r, ok := s.rows[sku.String()]
	if !ok {
		return nil, fmt.Errorf("SKU %q: %w", sku.String(), inventory.ErrStockItemNotFound)
	}
	// 確定済みの available は常に非負なのでエラーにはならない。
	qty, err := inventory.NewQuantity(r.available)
	if err != nil {
		return nil, fmt.Errorf("永続化された数量が不正です（SKU=%q）: %w", sku.String(), err)
	}
	loadedSKU, err := inventory.NewSKU(r.sku)
	if err != nil {
		return nil, fmt.Errorf("永続化された SKU が不正です: %w", err)
	}
	return inventory.ReconstituteStockItem(r.id, loadedSKU, qty, r.version), nil
}
