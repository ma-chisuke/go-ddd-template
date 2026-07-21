package aclhttp_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	invclient "github.com/example/go-ddd-template/clients/inventory/invclient"
	"github.com/example/go-ddd-template/contexts/ordering/internal/adapter/outbound/aclhttp"
	"github.com/example/go-ddd-template/contexts/ordering/internal/adapter/outbound/memory"
	"github.com/example/go-ddd-template/contexts/ordering/internal/application"
	"github.com/example/go-ddd-template/shared/correlation"
	"github.com/example/go-ddd-template/shared/uow"
)

// TestTraceIDFlowsPlaceToReserve は、注文作成（place）で context に載せた相関 ID が、
// ACL の在庫予約（reserve）呼び出しのヘッダとしてサービスを跨いで伝播することを検証する。
// これにより place -> reserve のフローが在庫サービスのログまで共有 trace_id で相関する。
func TestTraceIDFlowsPlaceToReserve(t *testing.T) {
	const traceID = "0af7651916cd43dd8448eb211c80319c" // 32 桁 16 進

	ts, captured := stubInventory(t, 200, "application/json", okAck, 0)
	reserver := aclhttp.NewReserver(mustClient(t, ts.URL))

	// 注文作成ユースケースをインメモリで組み立て、ACL だけ実 HTTP アダプタにする。
	store := memory.NewStore()
	obx := memory.NewOutboxStore()
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	place := application.NewPlaceOrder(
		uow.NewExecutor(uow.WithBaseBackoff(0)),
		memory.NewUnitOfWork(store, obx),
		reserver,
		application.NewInProcessDispatcher(log),
		log,
	)

	// 入口で確立された相関 ID を context に載せる（本番はミドルウェアが行う）。
	ctx := correlation.WithID(context.Background(), traceID)
	_, err := place.Handle(ctx, application.PlaceOrderInput{
		CustomerID: "CUST-1",
		Lines:      []application.PlaceOrderLine{{SKU: "SKU-A", Quantity: 1, UnitPriceAmount: 1000, Currency: "JPY"}},
	})
	require.NoError(t, err, "注文作成に失敗")

	// 在庫予約リクエストに相関 ID が伝播している。
	assert.Equal(t, traceID, captured.headers.Get("X-Correlation-ID"))
	// W3C traceparent の trace-id 部にも同じ trace_id が載っている。
	assert.Contains(t, captured.headers.Get("traceparent"), traceID, "traceparent は trace-id を含むべき")
}

func mustClient(t *testing.T, baseURL string) *invclient.Client {
	t.Helper()
	c, err := aclhttp.NewInventoryClient(baseURL, 5*time.Second)
	require.NoError(t, err, "クライアント生成失敗")
	return c
}
