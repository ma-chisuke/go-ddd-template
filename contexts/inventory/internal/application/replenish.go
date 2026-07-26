package application

import (
	"context"
	"errors"
	"log/slog"

	"github.com/example/go-ddd-template/contexts/inventory/internal/domain"
	"github.com/example/go-ddd-template/shared/id"
	"github.com/example/go-ddd-template/shared/uow"
)

// ReplenishInput は在庫補充ユースケースの入力。境界（HTTP など）から受け取った
// プリミティブ値を保持し、ユースケース内でドメインの値オブジェクトへ変換・検証する。
type ReplenishInput struct {
	SKU      string
	Quantity int
}

// StockResult は在庫の状態を表す出力 DTO。ドメインの値オブジェクトをそのまま外へ
// 漏らさず、プリミティブへ翻訳して返す。
type StockResult struct {
	SKU       string
	Available int
	Reserved  int
	Version   int
}

// Replenisher は在庫補充（書き込み）ユースケース。作業単位（UnitOfWork）の内側で
// 「読み込み → ドメイン操作 → 保存」を行い、楽観的排他制御の衝突時は Executor により
// 再試行される。
type Replenisher struct {
	exec     uow.Executor
	work     UnitOfWork
	dispatch EventDispatcher
	log      *slog.Logger
}

// NewReplenisher は在庫補充ユースケースを生成する。
// 引数 work は「uow」パッケージ名との混同を避けるため work と命名している。
func NewReplenisher(exec uow.Executor, work UnitOfWork, dispatch EventDispatcher, log *slog.Logger) *Replenisher {
	return &Replenisher{exec: exec, work: work, dispatch: dispatch, log: log}
}

// Replenish は指定 SKU の在庫を補充する。SKU が未登録なら在庫項目を新規作成してから補充する。
//
// ドメインイベントは外側の変数に退避し、作業単位が成功（uow.Run が nil を返す）した
// あとにのみ配信する。これにより「保存に失敗したのにイベントだけ配信される」ことを防ぐ。
func (r *Replenisher) Replenish(ctx context.Context, in ReplenishInput) (StockResult, error) {
	sku, err := domain.NewSKU(in.SKU)
	if err != nil {
		return StockResult{}, locate("", err)
	}
	qty, err := domain.NewQuantity(in.Quantity)
	if err != nil {
		return StockResult{}, locate("", err)
	}

	// クロージャの外で結果とイベントを退避する。再試行時は最後（成功時）の値で上書きされる。
	var events []domain.DomainEvent
	var result StockResult

	err = uow.Run(ctx, r.exec, r.work, func(ctx context.Context, repos Repos) error {
		item, err := repos.Stock().Load(ctx, sku)
		if err != nil {
			if !errors.Is(err, domain.ErrStockItemNotFound) {
				return err
			}
			// 未登録の SKU は新規の在庫項目として扱う。
			item, err = domain.NewStockItem(id.New(), sku)
			if err != nil {
				return err
			}
		}

		// 集約の不変条件（補充数量は 1 以上）。NewQuantity は 0 を通すのでここで弾かれる。
		if err := item.Replenish(qty); err != nil {
			return locate("", err)
		}
		if err := repos.Stock().Save(ctx, item); err != nil {
			return err
		}

		events = item.PullEvents()
		result = StockResult{
			SKU:       item.SKU().String(),
			Available: item.Available().Int(),
			Reserved:  item.Reserved().Int(),
			Version:   item.Version(),
		}
		return nil
	})
	if err != nil {
		return StockResult{}, err
	}

	// 作業単位が成功したあとにのみ、プロセス内でイベントを配信する。
	r.dispatch.Dispatch(ctx, events...)

	r.log.InfoContext(ctx, "在庫を補充しました",
		slog.String("sku", result.SKU),
		slog.Int("available", result.Available),
		slog.Int("version", result.Version),
	)
	return result, nil
}
