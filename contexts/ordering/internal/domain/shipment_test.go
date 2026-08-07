package domain_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/example/go-ddd-template/contexts/ordering/internal/domain"
)

func mustShipmentID(t *testing.T, s string) domain.ShipmentID {
	t.Helper()
	id, err := domain.NewShipmentID(s)
	require.NoError(t, err, "ShipmentID の生成に失敗しました")
	return id
}

func mustTrackingNumber(t *testing.T, s string) domain.TrackingNumber {
	t.Helper()
	n, err := domain.NewTrackingNumber(s)
	require.NoError(t, err, "TrackingNumber の生成に失敗しました")
	return n
}

// newPreparingShipment は preparing 状態の出荷を組み立てる。
func newPreparingShipment(t *testing.T) *domain.Shipment {
	t.Helper()
	s, err := domain.NewShipment(mustShipmentID(t, "SHIP-1"), mustOrderID(t, "ORDER-1"))
	require.NoError(t, err, "Shipment の生成に失敗しました")
	return s
}

func TestNewShipment(t *testing.T) {
	t.Parallel()

	t.Run("正常系: preparing・追跡番号ゼロ値・未永続化で始まる", func(t *testing.T) {
		t.Parallel()

		s := newPreparingShipment(t)
		assert.Equal(t, domain.StatusPreparing, s.Status(), "初期状態")
		assert.True(t, s.TrackingNumber().IsZero(), "preparing の間は追跡番号がゼロ値")
		assert.Equal(t, 0, s.Version(), "未永続化")
		assert.Equal(t, "ORDER-1", s.OrderID().String(), "注文は識別子で参照する")
		assert.Empty(t, s.PullEvents(), "生成ではイベントを発生させない")
	})

	t.Run("異常系: 出荷 ID がゼロ値なら ErrInvalidShipmentID", func(t *testing.T) {
		t.Parallel()

		_, err := domain.NewShipment(domain.ShipmentID{}, mustOrderID(t, "ORDER-1"))
		require.ErrorIs(t, err, domain.ErrInvalidShipmentID)
	})

	t.Run("異常系: 注文 ID がゼロ値なら ErrInvalidOrderID", func(t *testing.T) {
		t.Parallel()

		_, err := domain.NewShipment(mustShipmentID(t, "SHIP-1"), domain.OrderID{})
		require.ErrorIs(t, err, domain.ErrInvalidOrderID)
	})
}

func TestShipmentMarkShipped(t *testing.T) {
	t.Parallel()

	t.Run("正常系: preparing から shipped へ遷移し ShipmentDispatched を記録する", func(t *testing.T) {
		t.Parallel()

		s := newPreparingShipment(t)
		require.NoError(t, s.MarkShipped(mustTrackingNumber(t, "TRACK-1")))

		assert.Equal(t, domain.StatusShipped, s.Status(), "遷移後の状態")
		assert.Equal(t, "TRACK-1", s.TrackingNumber().String(), "追跡番号")

		events := s.PullEvents()
		require.Len(t, events, 1, "記録されたイベント数")
		assert.Equal(t, "ordering.shipment_dispatched", events[0].EventName())
		assert.False(t, events[0].OccurredAt().IsZero(), "発生時刻")
		assert.Empty(t, s.PullEvents(), "PullEvents は蓄積を空にする")
	})

	t.Run("異常系: shipped からの再呼び出しは ErrShipmentNotPreparing（冪等ではない）", func(t *testing.T) {
		t.Parallel()

		s := newPreparingShipment(t)
		require.NoError(t, s.MarkShipped(mustTrackingNumber(t, "TRACK-1")))

		err := s.MarkShipped(mustTrackingNumber(t, "TRACK-2"))
		require.ErrorIs(t, err, domain.ErrShipmentNotPreparing)
		// 状態も追跡番号も変わっていない（別の追跡番号で黙って上書きしない）。
		assert.Equal(t, domain.StatusShipped, s.Status(), "失敗後の状態")
		assert.Equal(t, "TRACK-1", s.TrackingNumber().String(), "失敗後の追跡番号")
	})

	t.Run("異常系: 追跡番号がゼロ値なら ErrInvalidTrackingNumber で状態を変えない", func(t *testing.T) {
		t.Parallel()

		s := newPreparingShipment(t)
		err := s.MarkShipped(domain.TrackingNumber{})
		require.ErrorIs(t, err, domain.ErrInvalidTrackingNumber)
		assert.Equal(t, domain.StatusPreparing, s.Status(), "失敗後の状態")
		assert.Empty(t, s.PullEvents(), "失敗時はイベントを記録しない")
	})
}

func TestReconstituteShipment(t *testing.T) {
	t.Parallel()

	s := domain.ReconstituteShipment(
		mustShipmentID(t, "SHIP-9"),
		mustOrderID(t, "ORDER-9"),
		domain.StatusShipped,
		mustTrackingNumber(t, "TRACK-9"),
		4,
	)
	assert.Equal(t, "SHIP-9", s.ID().String(), "ID")
	assert.Equal(t, "ORDER-9", s.OrderID().String(), "OrderID")
	assert.Equal(t, domain.StatusShipped, s.Status(), "Status")
	assert.Equal(t, "TRACK-9", s.TrackingNumber().String(), "TrackingNumber")
	assert.Equal(t, 4, s.Version(), "Version")
	assert.Empty(t, s.PullEvents(), "復元ではイベントを発生させない")

	s.MarkPersisted(5)
	assert.Equal(t, 5, s.Version(), "MarkPersisted 後の Version")
}

func TestShipmentStatusString(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "preparing", domain.StatusPreparing.String())
	assert.Equal(t, "shipped", domain.StatusShipped.String())
	assert.Equal(t, "unknown", domain.ShipmentStatus(99).String(), "未知の値は unknown")
}

func TestNewShipmentID_ZeroAndConstruction(t *testing.T) {
	t.Parallel()

	_, err := domain.NewShipmentID("   ")
	require.ErrorIs(t, err, domain.ErrInvalidShipmentID, "空白のみは拒否する")

	id := mustShipmentID(t, "  SHIP-7  ")
	assert.Equal(t, "SHIP-7", id.String(), "前後の空白を取り除く")
	assert.False(t, id.IsZero(), "生成された ID はゼロ値ではない")
	assert.True(t, domain.ShipmentID{}.IsZero(), "ゼロ値")
}

func TestNewTrackingNumber_ZeroAndConstruction(t *testing.T) {
	t.Parallel()

	_, err := domain.NewTrackingNumber("   ")
	require.ErrorIs(t, err, domain.ErrInvalidTrackingNumber, "空白のみは拒否する")

	n := mustTrackingNumber(t, "  TRACK-7  ")
	assert.Equal(t, "TRACK-7", n.String(), "前後の空白を取り除く")
	assert.False(t, n.IsZero(), "生成された追跡番号はゼロ値ではない")
	assert.True(t, domain.TrackingNumber{}.IsZero(), "ゼロ値")
}
