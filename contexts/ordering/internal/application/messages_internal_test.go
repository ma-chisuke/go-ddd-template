package application

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/example/go-ddd-template/contexts/ordering/internal/domain"
)

// これはパッケージ内部（application）テスト。非公開のメッセージ組み立て関数を直接検証する。

func TestToOutboxMessage_OrderPlacedHasNoRoute(t *testing.T) {
	t.Parallel()

	e := domain.OrderPlaced{OrderID: "ORDER-1", ReservationRef: "RES-1", At: time.Now().UTC()}
	_, ok, err := toOutboxMessage(e, "trace-1")
	require.NoError(t, err)
	assert.False(t, ok, "OrderPlaced はクロスコンテキストの送出経路を持たないはず")
}

func TestToOutboxMessage_OrderCancelled(t *testing.T) {
	t.Parallel()

	e := domain.OrderCancelled{OrderID: "ORDER-1", ReservationRef: "RES-1", At: time.Now().UTC()}
	m, ok, err := toOutboxMessage(e, "trace-1")
	require.NoError(t, err)
	require.True(t, ok, "OrderCancelled は送出経路を持つべき")
	assert.Equal(t, MessageTypeOrderCancelled, m.Type)
	assert.Equal(t, "trace-1", m.TraceID)
	assert.NotEmpty(t, m.ID)

	var p struct {
		ReservationRef string `json:"reservation_ref"`
		OrderID        string `json:"order_id"`
	}
	require.NoError(t, json.Unmarshal(m.Payload, &p))
	assert.Equal(t, "RES-1", p.ReservationRef)
	assert.Equal(t, "ORDER-1", p.OrderID)
}

func TestConfirmReservationMessage(t *testing.T) {
	t.Parallel()

	ref, err := domain.NewReservationRef("REF-1")
	require.NoError(t, err)

	m, err := confirmReservationMessage(ref, "trace-1")
	require.NoError(t, err)
	assert.Equal(t, MessageTypeConfirmReservation, m.Type)
	assert.Equal(t, "trace-1", m.TraceID)
	assert.NotEmpty(t, m.ID)
	assert.False(t, m.OccurredAt.IsZero())

	var p struct {
		ReservationRef string `json:"reservation_ref"`
	}
	require.NoError(t, json.Unmarshal(m.Payload, &p))
	assert.Equal(t, "REF-1", p.ReservationRef)
}
