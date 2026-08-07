package memory

import (
	"context"
	"fmt"
	"sync"

	"github.com/example/go-ddd-template/contexts/ordering/internal/application"
	"github.com/example/go-ddd-template/contexts/ordering/internal/domain"
	"github.com/example/go-ddd-template/shared/uow"
)

// shipmentRecord は確定済み（コミット済み）の出荷行。
type shipmentRecord struct {
	id       string
	orderID  string
	status   string
	tracking string
	version  int
}

// ShipmentRows は確定済みの出荷行を保持する backing store。並行アクセスを mutex で守る。
//
// **これは application.ShipmentStore ポートの実装ではない**（OrderRows と同じ位置づけ）。
// ポートを実装するのは txShipmentStore と readShipmentStore である。
type ShipmentRows struct {
	mu   sync.Mutex
	rows map[string]shipmentRecord // key: ShipmentID 文字列
}

// NewShipmentRows は空の出荷 backing store を生成する。
func NewShipmentRows() *ShipmentRows {
	return &ShipmentRows{rows: make(map[string]shipmentRecord)}
}

// load は確定済みデータから出荷を読み込み、集約を復元する。
func (r *ShipmentRows) load(id domain.ShipmentID) (*domain.Shipment, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	row, ok := r.rows[id.String()]
	if !ok {
		return nil, fmt.Errorf("出荷 %q: %w", id.String(), domain.ErrShipmentNotFound)
	}
	return recordToShipment(row)
}

// putLocked は確定済みデータへ 1 行を書き込む（**呼び出し側が mu を保持していること**）。
func (r *ShipmentRows) putLocked(row shipmentRecord) {
	r.rows[row.id] = row
}

// parseShipmentStatus は永続化された文字列を出荷状態へ変換する。
func parseShipmentStatus(s string) (domain.ShipmentStatus, error) {
	switch s {
	case "preparing":
		return domain.StatusPreparing, nil
	case "shipped":
		return domain.StatusShipped, nil
	default:
		return domain.StatusPreparing, fmt.Errorf("永続化された出荷状態が不正です: %q", s)
	}
}

// recordToShipment は確定済みの行から集約を復元する。
func recordToShipment(r shipmentRecord) (*domain.Shipment, error) {
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
	// preparing の追跡番号は空文字で保存されている。空を拒否する検証は「利用者が空を
	// 指定した」場合のためのものなので、未設定という正当な状態の復元には通さない。
	var tracking domain.TrackingNumber
	if r.tracking != "" {
		tracking, err = domain.NewTrackingNumber(r.tracking)
		if err != nil {
			return nil, fmt.Errorf("永続化された追跡番号が不正です: %w", err)
		}
	}
	return domain.ReconstituteShipment(shipmentID, orderID, status, tracking, r.version), nil
}

// shipmentToRecord は集約を、指定バージョンで確定行へ変換する。
func shipmentToRecord(s *domain.Shipment, version int) shipmentRecord {
	return shipmentRecord{
		id:       s.ID().String(),
		orderID:  s.OrderID().String(),
		status:   s.Status().String(),
		tracking: s.TrackingNumber().String(),
		version:  version,
	}
}

// txShipmentStore はトランザクションに束ねた ShipmentStore。
type txShipmentStore struct {
	tx   *txState
	rows *ShipmentRows
}

// コンパイル時にポートを満たしていることを確認する。
// この表明は検査 14（集約ストア実装のファイル名）の判定根拠でもある。
var _ application.ShipmentStore = (*txShipmentStore)(nil)

func (s *txShipmentStore) Load(_ context.Context, id domain.ShipmentID) (*domain.Shipment, error) {
	return s.rows.load(id)
}

// Save は集約の版を確定ストアと突き合わせて検証し、集約のバージョンを同期（MarkPersisted）
// したうえで、確定ストアへ書き込む操作を staging に積む。実際の書き込みはコミット時に行う。
// 版が食い違えば uow.ErrConcurrencyConflict を返し、確定ストアは変更しない。
//
// **注文ストアの Save と同型である。** 集約を 1 つ足しても staging 機構（txState / applyGroup）
// には手を入れない — 積み先の backing store が変わるだけである。
func (s *txShipmentStore) Save(_ context.Context, sh *domain.Shipment) error {
	s.rows.mu.Lock()
	defer s.rows.mu.Unlock()

	existing, ok := s.rows.rows[sh.ID().String()]
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

	row := shipmentToRecord(sh, next)
	s.tx.stage(&s.rows.mu, func() { s.rows.putLocked(row) })
	sh.MarkPersisted(next)
	return nil
}

// NewReadShipmentStore は読み取り用の ShipmentStore を返す。出荷照会ユースケース用。
// 書き込みには使わない。
func NewReadShipmentStore(rows *ShipmentRows) application.ShipmentStore {
	return &readShipmentStore{rows: rows}
}

// readShipmentStore は確定済みデータを直接読む読み取り専用アダプタ。
type readShipmentStore struct {
	rows *ShipmentRows
}

var _ application.ShipmentStore = (*readShipmentStore)(nil)

func (s *readShipmentStore) Load(_ context.Context, id domain.ShipmentID) (*domain.Shipment, error) {
	return s.rows.load(id)
}

// Save は読み取り専用アダプタでは使用しない。誤用を早期に検知するためエラーを返す。
func (s *readShipmentStore) Save(_ context.Context, _ *domain.Shipment) error {
	return fmt.Errorf("readShipmentStore は読み取り専用です: 書き込みは UnitOfWork.Within を使ってください")
}
