package application_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/example/go-ddd-template/contexts/ordering/internal/application"
	"github.com/example/go-ddd-template/contexts/ordering/internal/domain"
)

func TestGetShipment_Happy(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	f := newMemFixture(t)
	shipmentID := prepareShipmentFor(t, f)

	view, err := f.getShipment.Handle(ctx, shipmentID)

	require.NoError(t, err, "出荷照会")
	assert.Equal(t, shipmentID, view.ID, "ID")
	assert.NotEmpty(t, view.OrderID, "注文の識別子を運ぶ")
	assert.Equal(t, "preparing", view.Status, "状態")
	assert.Equal(t, 1, view.Version, "version")
}

func TestGetShipment_NotFound(t *testing.T) {
	t.Parallel()

	f := newMemFixture(t)

	_, err := f.getShipment.Handle(context.Background(), "MISSING-SHIPMENT")

	require.ErrorIs(t, err, domain.ErrShipmentNotFound, "番兵")
}

func TestGetShipment_InvalidID(t *testing.T) {
	t.Parallel()

	f := newMemFixture(t)

	_, err := f.getShipment.Handle(context.Background(), "   ")

	require.ErrorIs(t, err, domain.ErrInvalidShipmentID, "番兵")
	var ve *application.ValidationError
	require.ErrorAs(t, err, &ve, "入力検証エラーとして位置づけられる")
	require.Len(t, ve.Violations, 1, "違反は 1 件")
	assert.Equal(t, "ShipmentId", ve.Violations[0].Path, "入力 DTO 上のパス")
}
