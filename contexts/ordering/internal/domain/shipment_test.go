package domain_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/example/go-ddd-template/contexts/ordering/internal/domain"
)

func mustShipmentID(t *testing.T, s string) domain.ShipmentID {
	t.Helper()
	id, err := domain.NewShipmentID(s)
	require.NoError(t, err, "ShipmentID 生成")
	return id
}

func mustTrackingNumber(t *testing.T, s string) domain.TrackingNumber {
	t.Helper()
	tn, err := domain.NewTrackingNumber(s)
	require.NoError(t, err, "TrackingNumber 生成")
	return tn
}

func mustOrderIDForShipment(t *testing.T, s string) domain.OrderID {
	t.Helper()
	id, err := domain.NewOrderID(s)
	require.NoError(t, err, "OrderID 生成")
	return id
}

// newPreparingShipment は preparing の出荷を作る（version 0 = 未永続化）。
func newPreparingShipment(t *testing.T) *domain.Shipment {
	t.Helper()
	return domain.NewShipment(mustShipmentID(t, "SHIP-1"), mustOrderIDForShipment(t, "ORDER-1"))
}

func TestNewShipment_StartsPreparing(t *testing.T) {
	t.Parallel()

	s := newPreparingShipment(t)

	assert.Equal(t, "SHIP-1", s.ID().String(), "ID")
	assert.Equal(t, "ORDER-1", s.OrderID().String(), "OrderID（識別子による参照）")
	assert.Equal(t, domain.ShipmentPreparing, s.Status(), "初期状態は preparing")
	assert.True(t, s.TrackingNumber().IsZero(), "preparing の追跡番号はゼロ値")
	assert.Equal(t, 0, s.Version(), "新規作成の version は 0（未永続化）")
	assert.Empty(t, s.PullEvents(), "作成ではイベントを記録しない")
}

func TestShipment_MarkShippedStateMachine(t *testing.T) {
	t.Parallel()

	t.Run("正常系: preparing から shipped へ遷移し追跡番号を確定する", func(t *testing.T) {
		t.Parallel()

		s := newPreparingShipment(t)
		before := time.Now().UTC()

		require.NoError(t, s.MarkShipped(mustTrackingNumber(t, "TRACK-1")), "MarkShipped")

		assert.Equal(t, domain.ShipmentShipped, s.Status(), "遷移後の状態")
		assert.Equal(t, "TRACK-1", s.TrackingNumber().String(), "追跡番号")

		events := s.PullEvents()
		require.Len(t, events, 1, "ShipmentDispatched を 1 つ記録する")
		e, ok := events[0].(domain.ShipmentDispatched)
		require.True(t, ok, "記録されたのは ShipmentDispatched")
		assert.Equal(t, "SHIP-1", e.ShipmentID, "イベントの出荷 ID")
		assert.Equal(t, "ORDER-1", e.OrderID, "イベントは注文を識別子で運ぶ")
		assert.Equal(t, "TRACK-1", e.TrackingNumber, "イベントの追跡番号")
		assert.False(t, e.OccurredAt().Before(before), "発生時刻")
		assert.Equal(t, "ordering.shipment_dispatched", e.EventName(), "イベント種別名")

		assert.Empty(t, s.PullEvents(), "PullEvents は取り出したイベントを消す")
	})

	t.Run("異常系: shipped からの再発送は拒否され状態を変えない", func(t *testing.T) {
		t.Parallel()

		s := newPreparingShipment(t)
		require.NoError(t, s.MarkShipped(mustTrackingNumber(t, "TRACK-1")), "1 回目")
		_ = s.PullEvents()

		err := s.MarkShipped(mustTrackingNumber(t, "TRACK-2"))

		require.ErrorIs(t, err, domain.ErrShipmentNotPreparing, "番兵")
		assert.Equal(t, domain.ShipmentShipped, s.Status(), "状態は変わらない")
		assert.Equal(t, "TRACK-1", s.TrackingNumber().String(), "追跡番号は上書きされない")
		assert.Empty(t, s.PullEvents(), "拒否されたのでイベントは記録されない")
	})
}

func TestReconstituteShipment_RestoresWithoutEvents(t *testing.T) {
	t.Parallel()

	s := domain.ReconstituteShipment(
		mustShipmentID(t, "SHIP-9"),
		mustOrderIDForShipment(t, "ORDER-9"),
		domain.ShipmentShipped,
		mustTrackingNumber(t, "TRACK-9"),
		3,
	)

	assert.Equal(t, "SHIP-9", s.ID().String(), "ID")
	assert.Equal(t, "ORDER-9", s.OrderID().String(), "OrderID")
	assert.Equal(t, domain.ShipmentShipped, s.Status(), "状態")
	assert.Equal(t, "TRACK-9", s.TrackingNumber().String(), "追跡番号")
	assert.Equal(t, 3, s.Version(), "version")
	assert.Empty(t, s.PullEvents(), "復元ではイベントを発生させない")
}

func TestShipment_MarkPersistedSyncsVersion(t *testing.T) {
	t.Parallel()

	s := newPreparingShipment(t)
	require.Equal(t, 0, s.Version(), "前提: 未永続化")

	s.MarkPersisted(1)

	assert.Equal(t, 1, s.Version(), "リポジトリが同期した版を保持する")
}

func TestNewShipmentID_ZeroAndConstruction(t *testing.T) {
	t.Parallel()

	t.Run("正常系: 前後の空白を取り除いて包む", func(t *testing.T) {
		t.Parallel()

		id, err := domain.NewShipmentID("  SHIP-1  ")

		require.NoError(t, err, "生成")
		assert.Equal(t, "SHIP-1", id.String(), "String")
		assert.False(t, id.IsZero(), "IsZero")
	})

	t.Run("異常系: 空白のみは規則違反として拒否する", func(t *testing.T) {
		t.Parallel()

		_, err := domain.NewShipmentID("   ")

		require.ErrorIs(t, err, domain.ErrInvalidShipmentID, "番兵")
		var v *domain.FieldViolation
		require.ErrorAs(t, err, &v, "FieldViolation として取り出せる")
		assert.Equal(t, "shipmentId", v.Rule.Field, "Rule.Field")
		assert.Equal(t, "invalid_shipment_id", v.Rule.Code, "Rule.Code")
	})

	t.Run("境界: ゼロ値は IsZero が真", func(t *testing.T) {
		t.Parallel()

		var id domain.ShipmentID

		assert.True(t, id.IsZero(), "IsZero")
		assert.Empty(t, id.String(), "String")
	})
}

func TestNewTrackingNumber_ZeroAndConstruction(t *testing.T) {
	t.Parallel()

	t.Run("正常系: 前後の空白を取り除いて包む", func(t *testing.T) {
		t.Parallel()

		tn, err := domain.NewTrackingNumber("  TRACK-1  ")

		require.NoError(t, err, "生成")
		assert.Equal(t, "TRACK-1", tn.String(), "String")
		assert.False(t, tn.IsZero(), "IsZero")
	})

	t.Run("異常系: 空白のみは規則違反として拒否する", func(t *testing.T) {
		t.Parallel()

		_, err := domain.NewTrackingNumber("   ")

		require.ErrorIs(t, err, domain.ErrInvalidTrackingNumber, "番兵")
		var v *domain.FieldViolation
		require.ErrorAs(t, err, &v, "FieldViolation として取り出せる")
		assert.Equal(t, "trackingNumber", v.Rule.Field, "Rule.Field")
		assert.Equal(t, "invalid_tracking_number", v.Rule.Code, "Rule.Code")
	})

	t.Run("境界: ゼロ値は IsZero が真", func(t *testing.T) {
		t.Parallel()

		var tn domain.TrackingNumber

		assert.True(t, tn.IsZero(), "IsZero")
		assert.Empty(t, tn.String(), "String")
	})
}

func TestShipmentStatusString(t *testing.T) {
	t.Parallel()

	cases := map[domain.ShipmentStatus]string{
		domain.ShipmentPreparing: "preparing",
		domain.ShipmentShipped:   "shipped",
		domain.ShipmentStatus(9): "unknown",
	}
	for s, want := range cases {
		assert.Equal(t, want, s.String(), "ShipmentStatus(%d).String()", int(s))
	}
}
