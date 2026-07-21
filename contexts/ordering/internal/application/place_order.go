package application

import (
	"context"
	"log/slog"

	"github.com/example/go-ddd-template/contexts/ordering/internal/domain/order"
	"github.com/example/go-ddd-template/contexts/ordering/port"
	"github.com/example/go-ddd-template/shared/correlation"
	"github.com/example/go-ddd-template/shared/id"
	"github.com/example/go-ddd-template/shared/uow"
)

// PlaceOrderLine は注文作成の 1 行の入力（境界から受け取ったプリミティブ値）。
type PlaceOrderLine struct {
	SKU             string
	Quantity        int
	UnitPriceAmount int64
	Currency        string
}

// PlaceOrderInput は注文作成ユースケースの入力。
type PlaceOrderInput struct {
	CustomerID string
	Lines      []PlaceOrderLine
}

// PlaceOrder は注文作成（二相予約）ユースケース。
//
// フェーズ 1: 在庫を **同期** 予約する（ACL・トランザクションの外）。予約が拒否されたら
// 注文を作らずに失敗させる。フェーズ 2: 同一の作業単位で注文を Confirmed 保存し、
// ConfirmReservation コマンドをアウトボックスへ積む。注文が durable になれば確定は
// at-least-once で必ず届く。フェーズ 2 の保存が失敗したら best-effort な補償解放を試み、
// 在庫側の pending TTL が backstop になる。
type PlaceOrder struct {
	exec     uow.Executor
	work     UnitOfWork
	reserver StockReserver
	dispatch EventDispatcher
	log      *slog.Logger
}

// NewPlaceOrder は注文作成ユースケースを生成する。
func NewPlaceOrder(exec uow.Executor, work UnitOfWork, reserver StockReserver, dispatch EventDispatcher, log *slog.Logger) *PlaceOrder {
	return &PlaceOrder{exec: exec, work: work, reserver: reserver, dispatch: dispatch, log: log}
}

// Handle は注文を作成する。成功すると作成された注文の ID を返す。
//
// エラー:
//   - 明細が空 / 数量・金額が不正 → ドメインのセンチネル（ErrEmptyOrder など）。
//   - 在庫予約の拒否 / 在庫サービス不達 → ErrReservationRejected / ErrReservationUnavailable。
func (uc *PlaceOrder) Handle(ctx context.Context, in PlaceOrderInput) (order.OrderID, error) {
	customer, err := order.NewCustomerID(in.CustomerID)
	if err != nil {
		return order.OrderID{}, err
	}
	lines, reserveLines, err := toOrderLines(in.Lines)
	if err != nil {
		return order.OrderID{}, err
	}

	orderID, err := order.NewOrderID(id.New())
	if err != nil {
		return order.OrderID{}, err
	}
	o, err := order.NewOrder(orderID, customer, lines)
	if err != nil {
		return order.OrderID{}, err
	}
	ref := o.ReservationRef()

	// フェーズ 1: 在庫を同期予約する。ACL はトランザクションの外で呼ぶ（Repos に含めない）。
	// 予約が拒否・不達なら、注文を作らずに失敗を即時ユーザーへ返す。
	if err := uc.reserver.Reserve(ctx, ref.String(), reserveLines); err != nil {
		return order.OrderID{}, err
	}

	traceID := correlation.FromContextOrEmpty(ctx)
	confirmMsg, err := confirmReservationMessage(ref, traceID)
	if err != nil {
		// メッセージ組み立ての失敗は予約成立後なので、補償解放を試みてから返す。
		uc.releaseCompensating(ctx, ref)
		return order.OrderID{}, err
	}

	// フェーズ 2: 同一 UoW で注文を Confirmed 保存し、ConfirmReservation コマンドを Enqueue する。
	err = uow.Run(ctx, uc.exec, uc.work, func(ctx context.Context, repos Repos) error {
		if err := repos.Orders().Save(ctx, o); err != nil {
			return err
		}
		return repos.Outbox().Enqueue(ctx, confirmMsg)
	})
	if err != nil {
		// 保存失敗時は best-effort な補償解放を試みる（在庫側の pending TTL が backstop）。
		uc.releaseCompensating(ctx, ref)
		return order.OrderID{}, err
	}

	// コミット後にプロセス内イベント（OrderPlaced）を配信する。
	uc.dispatch.Dispatch(ctx, o.PullEvents()...)
	uc.log.InfoContext(ctx, "注文を作成しました",
		slog.String("order_id", orderID.String()),
		slog.String("ref", ref.String()),
	)
	return orderID, nil
}

// releaseCompensating は保存失敗時の補償解放を best-effort で試みる。
// 失敗しても在庫側の pending TTL が孤児 pending を回収するため、ここではログに留める。
func (uc *PlaceOrder) releaseCompensating(ctx context.Context, ref order.ReservationRef) {
	if err := uc.reserver.Release(ctx, ref.String()); err != nil {
		uc.log.WarnContext(ctx, "補償解放に失敗しました（在庫側の pending TTL が backstop）",
			slog.String("ref", ref.String()),
			slog.Any("error", err),
		)
	}
}

// toOrderLines は入力行を、ドメインの注文明細と ACL 用の予約行の双方へ変換・検証する。
// 予約行（port.ReserveLine）は翻訳済み DTO であり、在庫側へはこの形で渡す。
func toOrderLines(in []PlaceOrderLine) ([]order.OrderLine, []port.ReserveLine, error) {
	lines := make([]order.OrderLine, 0, len(in))
	reserveLines := make([]port.ReserveLine, 0, len(in))
	for _, l := range in {
		sku, err := order.NewSKU(l.SKU)
		if err != nil {
			return nil, nil, err
		}
		qty, err := order.NewQuantity(l.Quantity)
		if err != nil {
			return nil, nil, err
		}
		price, err := order.NewMoney(l.UnitPriceAmount, l.Currency)
		if err != nil {
			return nil, nil, err
		}
		lines = append(lines, order.NewOrderLine(sku, qty, price))
		reserveLines = append(reserveLines, port.ReserveLine{SKU: sku.String(), Qty: qty.Int()})
	}
	return lines, reserveLines, nil
}
