package memory

import (
	"context"
	"fmt"

	"github.com/example/go-ddd-template/contexts/ordering/internal/application"
	"github.com/example/go-ddd-template/contexts/ordering/internal/domain"
	"github.com/example/go-ddd-template/shared/uow"
)

// shipmentStore はトランザクションに束ねた ShipmentStore。
// 読み取りは確定済みの行を見る（同一トランザクションで staging した行は見えない）。
//
// orderStore と同じ形をしている。新しい集約を足すときに書くのはこのファイルだけで、
// 共通機構（rows.go）にも確定処理（uow.go の txState）にも差分は出ない。
type shipmentStore struct {
	rows    *ShipmentRows
	staging *staging[shipmentRow]
}

// コンパイル時にポートを満たしていることを確認する。
var _ application.ShipmentStore = (*shipmentStore)(nil)

func (s *shipmentStore) Load(_ context.Context, id domain.ShipmentID) (*domain.Shipment, error) {
	return loadShipment(s.rows, id)
}

// Save は集約の版を確定済みの行と突き合わせて検証し、集約のバージョンを同期
// （MarkPersisted）したうえで、書き込む行を staging に積む。実際の書き込みはコミット時に
// 行う。版が食い違えば uow.ErrConcurrencyConflict を返し、確定済みの行は変更しない。
//
// 版の読み取りと staging への積み込みを withLock で不可分に行う（orderStore.Save と同じ）。
func (s *shipmentStore) Save(_ context.Context, sh *domain.Shipment) error {
	return s.rows.withLock(func(m map[string]shipmentRow) error {
		existing, ok := m[sh.ID().String()]
		var next int
		if sh.Version() == 0 {
			// 新規挿入。既に存在するなら衝突。
			if ok {
				return fmt.Errorf("出荷 %q は既に存在します: %w", sh.ID().String(), uow.ErrConcurrencyConflict)
			}
			next = 1
		} else {
			// 既存更新。存在しない、または版が食い違えば衝突。
			if !ok || existing.version != sh.Version() {
				return fmt.Errorf("出荷 %q のバージョンが一致しません: %w", sh.ID().String(), uow.ErrConcurrencyConflict)
			}
			next = sh.Version() + 1
		}

		s.staging.stage(sh.ID().String(), shipmentToShipmentRow(sh, next))
		sh.MarkPersisted(next)
		return nil
	})
}

// NewReadShipmentStore は読み取り用の ShipmentStore を返す。出荷照会ユースケース用。
// 書き込みには使わない。
func NewReadShipmentStore(shipmentRows *ShipmentRows) application.ShipmentStore {
	return &readShipmentStore{rows: shipmentRows}
}

// readShipmentStore は確定済みデータを直接読む読み取り専用アダプタ。
type readShipmentStore struct {
	rows *ShipmentRows
}

var _ application.ShipmentStore = (*readShipmentStore)(nil)

func (s *readShipmentStore) Load(_ context.Context, id domain.ShipmentID) (*domain.Shipment, error) {
	return loadShipment(s.rows, id)
}

// Save は読み取り専用アダプタでは使用しない。誤用を早期に検知するためエラーを返す。
func (s *readShipmentStore) Save(_ context.Context, _ *domain.Shipment) error {
	return fmt.Errorf("readShipmentStore は読み取り専用です: 書き込みは UnitOfWork.Within を使ってください")
}

// loadShipment は確定済みの行から出荷集約を復元する。共通機構 rows[R] は shipmentRow の
// 中身を知らないため、この集約固有の読み取りはストア側に置く。
func loadShipment(shipmentRows *ShipmentRows, id domain.ShipmentID) (*domain.Shipment, error) {
	r, ok := shipmentRows.get(id.String())
	if !ok {
		return nil, fmt.Errorf("出荷 %q: %w", id.String(), domain.ErrShipmentNotFound)
	}
	return shipmentRowToShipment(r)
}
