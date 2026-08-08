//go:build integration

// 出荷アダプタの統合テスト。build タグ `integration` を付けたときだけ実行される。
//
// **CI はこのファイルを `-run='^$'` でコンパイルするだけで実行しない。** 実 DB でしか
// 壊れない性質（トランザクション意味論・楽観的排他の compare-and-set・schema 適用順）は
// ローカルでの実行に依存している。集約を足したら必ずここを実際に走らせること。
package postgres_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/example/go-ddd-template/contexts/ordering/internal/adapter/outbound/postgres"
	"github.com/example/go-ddd-template/contexts/ordering/internal/application"
	"github.com/example/go-ddd-template/contexts/ordering/internal/domain"
	"github.com/example/go-ddd-template/shared/event"
	"github.com/example/go-ddd-template/shared/id"
	"github.com/example/go-ddd-template/shared/uow"
)

// shipmentRowOf は出荷 1 行の状態を実 DB から読む（アダプタを経由せず SQL で直接見る）。
// 0 行なら ok = false。
func shipmentRowOf(t *testing.T, pool *pgxpool.Pool, id string) (status, tracking string, version int, ok bool) {
	t.Helper()
	err := pool.QueryRow(context.Background(),
		"SELECT status, tracking_number, version FROM ordering.shipments WHERE id = $1", id).
		Scan(&status, &tracking, &version)
	if err != nil {
		return "", "", 0, false
	}
	return status, tracking, version, true
}

// newShipmentUseCases は出荷ユースケースを postgres アダプタで組み立てる。
func newShipmentUseCases(t *testing.T, pool *pgxpool.Pool) (*application.PrepareShipment, *application.MarkShipped, *postgres.UnitOfWork) {
	t.Helper()
	log := testLogger()
	work := postgres.NewUnitOfWork(pool)
	exec := uow.NewExecutor()
	read := postgres.NewReadOrderStore(pool)
	dispatcher := event.NewTyped[domain.DomainEvent](log)
	return application.NewPrepareShipment(exec, work, read, log),
		application.NewMarkShipped(exec, work, dispatcher, log),
		work
}

// ロールバックすると出荷が 1 行も残らず、コミットすると preparing で 1 行残ることを
// **1 回の観測で同時に**確かめる。片方だけなら壊れた実装でも満たせる
// （常にロールバックする実装は EMPTY を、常にコミットする実装は PRESENT を満たす）。
func TestPostgres_PrepareShipmentRollbackAndCommit(t *testing.T) {
	ctx := context.Background()
	pool := setupPool(t)
	f := newPgFixture(t, pool)
	prepare, _, work := newShipmentUseCases(t, pool)

	orderID, err := f.place.Handle(ctx, sampleInput())
	require.NoError(t, err, "前提の注文作成")

	// 判定器の校正: 同じ数え方で「在るはずのもの」が数えられることを先に確かめる。
	// これをしないと、後段の 0 が「本当に 0 行」なのか「表名・スキーマ名を間違えて空」なのか
	// 区別できない。
	require.Equal(t, 1, countRows(t, pool, "ordering.orders"), "対照: 注文は 1 行ある")
	require.Zero(t, countRows(t, pool, "ordering.shipments"), "前提: 出荷はまだ無い")

	// --- EMPTY 側: ロールバックすると 1 行も残らない ---
	sentinel := errors.New("業務都合で中断")
	rolled, err := domain.NewShipmentID(id.New())
	require.NoError(t, err, "出荷 ID 生成")
	err = work.Within(ctx, func(ctx context.Context, r application.Repos) error {
		if err := r.Shipments().Save(ctx, domain.NewShipment(rolled, orderID)); err != nil {
			return err
		}
		return sentinel // ここで中断 -> ロールバック
	})
	require.ErrorIs(t, err, sentinel, "中断のエラーが伝播する")

	emptyCount := countRows(t, pool, "ordering.shipments")

	// --- PRESENT 側: コミットすると preparing で 1 行残る ---
	committed, err := prepare.Handle(ctx, orderID.String())
	require.NoError(t, err, "出荷準備")

	presentCount := countRows(t, pool, "ordering.shipments")
	status, tracking, version, ok := shipmentRowOf(t, pool, committed.String())

	// 観測した対を記録する（何を見て判断したかが実行ログに残るように）。
	t.Logf("観測: ロールバック後 ordering.shipments=%d 行 / コミット後 %d 行 status=%q tracking=%q version=%d",
		emptyCount, presentCount, status, tracking, version)

	// 対を 1 回の観測で突き合わせる。
	assert.Zero(t, emptyCount, "EMPTY: ロールバック時は ordering.shipments が 0 行")
	assert.Equal(t, 1, presentCount, "PRESENT: コミット時は ordering.shipments が 1 行")
	require.True(t, ok, "PRESENT: コミットした出荷が実 DB に在る")
	assert.Equal(t, "preparing", status, "PRESENT: status")
	assert.Empty(t, tracking, "PRESENT: preparing の追跡番号は空")
	assert.Equal(t, 1, version, "PRESENT: 永続化済みの版は 1")

	// ロールバックした方の ID は存在しない（消えたのは「全部」ではなく「その 1 件」である）。
	_, _, _, rolledOK := shipmentRowOf(t, pool, rolled.String())
	assert.False(t, rolledOK, "ロールバックした出荷は実 DB に無い")
}

// 発送は出荷を更新するが、アウトボックスへは 1 件も積まない（BR-S8）ことを、
// **配送キューの行数の差分**で確かめる。
//
// EMPTY を「0 行」で主張してはならない: PrepareShipment は confirmed の注文を要求するので
// シナリオは必ず PlaceOrder から始まり、PlaceOrder は同一 UoW で ConfirmReservation を
// outbox へ積む。リレーを回さない限り outbox は 0 行にならない。測るべきは差分である。
func TestPostgres_MarkShippedDoesNotEnqueueOutbox(t *testing.T) {
	ctx := context.Background()
	pool := setupPool(t)
	f := newPgFixture(t, pool)
	prepare, markShipped, _ := newShipmentUseCases(t, pool)

	orderID, err := f.place.Handle(ctx, sampleInput())
	require.NoError(t, err, "前提の注文作成")
	shipmentID, err := prepare.Handle(ctx, orderID.String())
	require.NoError(t, err, "前提の出荷準備")

	outboxBefore := countRows(t, pool, "ordering.outbox")
	eventsBefore := countRows(t, pool, "ordering.events")
	// EMPTY 側の前提を確立する。0 と 0 を比べても「増えていない」は言えない。
	require.Positive(t, outboxBefore, "前提: PlaceOrder が ConfirmReservation を積んでいる")
	_, _, versionBefore, ok := shipmentRowOf(t, pool, shipmentID.String())
	require.True(t, ok, "前提: 出荷が在る")

	require.NoError(t, markShipped.Handle(ctx, shipmentID.String(), "TRACK-1"), "発送")

	outboxAfter := countRows(t, pool, "ordering.outbox")
	eventsAfter := countRows(t, pool, "ordering.events")
	status, tracking, versionAfter, ok := shipmentRowOf(t, pool, shipmentID.String())

	t.Logf("観測: outbox %d -> %d 行 / events %d -> %d 行 / shipments status=%q tracking=%q version=%d -> %d",
		outboxBefore, outboxAfter, eventsBefore, eventsAfter, status, tracking, versionBefore, versionAfter)

	// EMPTY: 配送キューも恒久ログも増えない。
	assert.Equal(t, outboxBefore, outboxAfter, "EMPTY: MarkShipped の前後で ordering.outbox の行数が増えない")
	assert.Equal(t, eventsBefore, eventsAfter, "EMPTY: ordering.events も増えない")
	// PRESENT: 出荷自身は確かに更新されている。
	require.True(t, ok, "PRESENT: 出荷が在る")
	assert.Equal(t, "shipped", status, "PRESENT: status")
	assert.Equal(t, "TRACK-1", tracking, "PRESENT: tracking_number")
	assert.Equal(t, versionBefore+1, versionAfter, "PRESENT: 楽観的排他の版が 1 つ進む")
}
