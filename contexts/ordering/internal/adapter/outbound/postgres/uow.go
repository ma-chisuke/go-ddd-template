package postgres

import (
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/example/go-ddd-template/contexts/ordering/internal/adapter/outbound/postgres/sqlcgen"
	"github.com/example/go-ddd-template/contexts/ordering/internal/application"
	"github.com/example/go-ddd-template/shared/uow/pgxuow"
)

// repos は application.Repos の実装。ひとつのトランザクションに束ねた
// リポジトリの束（注文ストア・出荷ストアとアウトボックス）を保持する。
type repos struct {
	orders    application.OrderStore
	shipments application.ShipmentStore
	outbox    application.MessagePublisher
}

func (r repos) Orders() application.OrderStore       { return r.orders }
func (r repos) Shipments() application.ShipmentStore { return r.shipments }
func (r repos) Outbox() application.MessagePublisher { return r.outbox }

// UnitOfWork は pgxpool を用いた実トランザクションの作業単位。
// トランザクション・ライフサイクル（Begin/Commit/Rollback）は共有の pgx ドライバ
// 実装 shared/uow/pgxuow に集約してあり、ここでは型エイリアスで従来の型名を温存する。
// これにより NewUnitOfWork の戻り型名も統合テストの *postgres.UnitOfWork 参照も
// そのまま有効で、呼び出し側・テストの変更は発生しない。R は注文コンテキストの
// リポジトリ束 application.Repos に固定する。
type UnitOfWork = pgxuow.UnitOfWork[application.Repos]

// NewUnitOfWork は書き込み用の作業単位を生成する。トランザクションに束ねた Queries
// から注文コンテキストの Repos 束を組み立てる buildRepos クロージャだけを供給する
// 薄い factory であり、トランザクション境界の所有は pgxuow.Within が担う。
// 注文ストアとアウトボックスが同一トランザクションに参加する（原子的コミット）。
func NewUnitOfWork(pool *pgxpool.Pool) *UnitOfWork {
	return pgxuow.New(pool, func(tx pgx.Tx) application.Repos {
		q := sqlcgen.New(tx)
		return repos{orders: newOrderStore(q), shipments: newShipmentStore(q), outbox: newOutboxStore(q)}
	})
}

// コンパイル時にポートを満たしていることを確認する。
// UnitOfWork（= pgxuow.UnitOfWork[application.Repos]）が application.UnitOfWork
// （= uow.UnitOfWork[Repos]）を満たすことを、具象 R とともにここで検証する。
var _ application.UnitOfWork = (*UnitOfWork)(nil)

// NewReadOrderStore はコネクションプールに直結した読み取り用 OrderStore を返す。
// 読み取り専用ユースケース（注文照会）で用いる。書き込みには使わないこと
// （書き込みは必ず UnitOfWork.Within の内側で行う）。
func NewReadOrderStore(pool *pgxpool.Pool) application.OrderStore {
	return newOrderStore(sqlcgen.New(pool))
}

// NewReadShipmentStore はコネクションプールに直結した読み取り用 ShipmentStore を返す。
// 読み取り専用ユースケース（出荷照会）で用いる。書き込みには使わないこと
// （書き込みは必ず UnitOfWork.Within の内側で行う）。
func NewReadShipmentStore(pool *pgxpool.Pool) application.ShipmentStore {
	return newShipmentStore(sqlcgen.New(pool))
}
