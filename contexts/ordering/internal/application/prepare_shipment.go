package application

import (
	"context"
	"log/slog"

	"github.com/example/go-ddd-template/contexts/ordering/internal/domain"
	"github.com/example/go-ddd-template/shared/id"
	"github.com/example/go-ddd-template/shared/uow"
)

// PrepareShipment は出荷準備ユースケース。
//
// フェーズ 0（トランザクションの外）: 注文が存在し confirmed であることを確認する。
// フェーズ 1（トランザクションの内）: Shipment を生成して保存する。
//
// **注文をトランザクションの外で読むのは意図的である。** トランザクションの内側で触れる
// 集約ルートは Shipment ただ 1 つで、Order は識別子で参照するだけである
// （Repos.Orders() はこのユースケースでは使わない）。既存の PlaceOrder が在庫予約の
// ACL 呼び出しを uow.Run の外に出しているのと同じ形である。
//
// **競合を受け入れる**: フェーズ 0 で confirmed を確認してからフェーズ 1 のコミットまでの間に
// 注文が取り消される可能性がある。防ぐには Order と Shipment を同一トランザクションで
// 扱う必要があり、「1 トランザクション 1 集約」に反する。取り消された注文に対する出荷が
// preparing のまま残りうるが、(1) DB に FK を張らないので注文行の有無に依存せず、
// (2) 取消と出荷の相関は trace_id によるログ相関で運用側が追える。補償処理（出荷の自動取消）は
// このテンプレートに**無い** — 採用者が業務要件に応じて足す拡張点である。
type PrepareShipment struct {
	exec     uow.Executor
	work     UnitOfWork
	readOrd  OrderStore
	dispatch EventDispatcher
	log      *slog.Logger
}

// NewPrepareShipment は出荷準備ユースケースを生成する。
// readOrd はトランザクションを経由しない読み取り用の注文ストアである。
func NewPrepareShipment(exec uow.Executor, work UnitOfWork, readOrd OrderStore, dispatch EventDispatcher, log *slog.Logger) *PrepareShipment {
	return &PrepareShipment{exec: exec, work: work, readOrd: readOrd, dispatch: dispatch, log: log}
}

// Handle は指定した注文に対する出荷を準備する。成功すると作成された出荷の現在状態を返す。
//
// エラー:
//   - 注文 ID が不正 → domain.ErrInvalidOrderID（422）。
//   - 注文が存在しない → domain.ErrOrderNotFound（404）。
//   - 注文が confirmed でない → ErrOrderNotConfirmedForShipment（409）。
func (uc *PrepareShipment) Handle(ctx context.Context, orderIDStr string) (ShipmentView, error) {
	// **ここで locate を呼ばない**（意図的。理由を残す）。
	//
	// locate はドメインの違反を入力 DTO 上のパス（"OrderId"）へ解決し、インターフェース層の
	// jsonNames がそれを JSON 名へ写す。ところが jsonNames は**文脈を持たない写像**であり、
	// このコンテキストでは "OrderId" -> "id" と定義されている（getOrder / cancelOrder の
	// パスパラメータ /orders/{id} のため）。prepareShipment の orderId は**本文のフィールド**
	// なので、同じ写像を通すと invalid-params に "id" という誤った名前を載せてしまう。
	//
	// 誤った位置を主張するくらいなら位置を主張しない。ステータス（422）・type URI
	// （invalid-input）・detail は変わらず、invalid-params だけがキーごと省略される
	// （契約が「フィールドを特定できない場合は省略する」と定めている形）。なお本文の
	// orderId は契約の minLength: 1 が空文字を 400 で弾くため、この経路に到達するのは
	// 空白のみの文字列だけである。
	//
	// 恒久的な解決には「同じドメインフィールドが操作によって別の JSON 名に写る」ことを
	// 表現できる仕組みが要る（docs/add-an-aggregate.md の落とし穴 4 に記載）。
	orderID, err := domain.NewOrderID(orderIDStr)
	if err != nil {
		return ShipmentView{}, err
	}

	// フェーズ 0（tx の外）: 注文の存在と状態を確認する。
	o, err := uc.readOrd.Load(ctx, orderID)
	if err != nil {
		return ShipmentView{}, err
	}
	if o.Status() != domain.StatusConfirmed {
		return ShipmentView{}, ErrOrderNotConfirmedForShipment
	}

	// 採番はアプリケーション層が行う。ここで失敗したらリクエスターの入力ではなく
	// サーバ側の問題なので、locate で「入力検証エラー」に見せかけてはならない。
	shipmentID, err := domain.NewShipmentID(id.New())
	if err != nil {
		return ShipmentView{}, err
	}
	// 両 ID とも検証済みなのでここは通常到達しない。防御的に扱う（位置の解決は上と同じ理由で
	// 行わない — この経路の違反は利用者入力ではなく採番の失敗を意味する）。
	sh, err := domain.NewShipment(shipmentID, orderID)
	if err != nil {
		return ShipmentView{}, err
	}

	// フェーズ 1（tx の内）: 出荷だけを保存する。クロスコンテキストへの送信は無いので
	// アウトボックスへは積まない（在庫コンテキストは出荷を知る必要がない）。
	err = uow.Run(ctx, uc.exec, uc.work, func(ctx context.Context, repos Repos) error {
		return repos.Shipments().Save(ctx, sh)
	})
	if err != nil {
		return ShipmentView{}, err
	}

	// コミット後にプロセス内イベントを配信する（準備時点では発生しないが、経路は同じ）。
	uc.dispatch.Dispatch(ctx, sh.PullEvents()...)
	uc.log.InfoContext(ctx, "出荷を準備しました",
		slog.String("shipment_id", shipmentID.String()),
		slog.String("order_id", orderID.String()),
	)
	return toShipmentView(sh), nil
}
