//go:build integration

// マルチ SKU 予約の退行確認。build タグ `integration` を付けたときだけ実行される。
//
// **これは FR-3.2 の直撃点である。** memory アダプタの確定処理を集約非依存へ汎化したとき、
// 「同一 backing store への複数行の書き込みが不可分に確定する」性質を壊していないかは、
// 単一行しか書かない集約（Shipment）のテストでは検出できない。複数の StockItem を
// 1 回の Save で書く経路を実 DB でも通しておく。
package postgres_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/example/go-ddd-template/contexts/inventory/internal/application"
	"github.com/example/go-ddd-template/contexts/inventory/internal/domain"
	"github.com/example/go-ddd-template/shared/clock"
)

// mustQty は数量を生成する（このファイルのテスト専用ヘルパー）。
func mustQty(t *testing.T, n int) domain.Quantity {
	t.Helper()
	q, err := domain.NewQuantity(n)
	require.NoError(t, err, "Quantity 生成")
	return q
}

// versionsOf は指定 SKU 群の版を実 DB から SQL で直接読む（SKU -> version）。
func versionsOf(t *testing.T, pool *pgxpool.Pool, skus ...string) map[string]int {
	t.Helper()
	out := make(map[string]int, len(skus))
	for _, sku := range skus {
		var v int
		err := pool.QueryRow(context.Background(),
			"SELECT version FROM inventory.stock_items WHERE sku = $1", sku).Scan(&v)
		require.NoError(t, err, "版の取得（SKU=%s）", sku)
		out[sku] = v
	}
	return out
}

// マルチ SKU の保存が全か無かで確定することを、ロールバックとコミットの対で確かめる。
//
//   - EMPTY: ロールバック時は**全対象行が版据え置き**（1 行だけ進む、が起きない）
//   - PRESENT: コミット時は**全対象行が同一版へ**進む
//
// 片方だけなら壊れた実装でも満たせる（何も書かない実装は EMPTY を、
// 行ごとに確定する実装でも PRESENT の「進む」だけは満たしうる）。
func TestPostgres_MultiSKUSaveIsAllOrNothing(t *testing.T) {
	ctx := context.Background()
	pool := setupPool(t)
	f := newPgFixture(t, pool, clock.System{})

	skus := []string{"MULTI-A", "MULTI-B", "MULTI-C"}
	for _, sku := range skus {
		_, err := f.replenisher.Replenish(ctx, application.ReplenishInput{SKU: sku, Quantity: 10})
		require.NoError(t, err, "前提の補充（SKU=%s）", sku)
	}

	// 判定器の校正: 版を数える経路が実際に値を返すことを先に確かめる。
	// これをしないと、後段の「据え置き」が「本当に変わっていない」のか
	// 「SKU 名を間違えて同じ値を読み続けている」のか区別できない。
	before := versionsOf(t, pool, skus...)
	for _, sku := range skus {
		require.Equal(t, 1, before[sku], "対照: 補充直後の版は 1（SKU=%s）", sku)
	}

	domainSKUs := make([]domain.SKU, 0, len(skus))
	for _, s := range skus {
		domainSKUs = append(domainSKUs, mustSKU(t, s))
	}
	ref, err := domain.NewReservationRef("MULTI-REF-1")
	require.NoError(t, err, "予約参照")

	// --- EMPTY 側: 3 件すべてを予約して保存したうえで中断する ---
	sentinel := errors.New("業務都合で中断")
	err = f.work.Within(ctx, func(ctx context.Context, r application.Repos) error {
		items, err := r.Stock().LoadMany(ctx, domainSKUs)
		if err != nil {
			return err
		}
		require.Len(t, items, len(skus), "3 件とも読めている")
		for _, item := range items {
			if err := item.Reserve(ref, mustQty(t, 1), 0); err != nil {
				return err
			}
		}
		if err := r.Stock().Save(ctx, items...); err != nil {
			return err
		}
		return sentinel // ここで中断 -> ロールバック
	})
	require.ErrorIs(t, err, sentinel, "中断のエラーが伝播する")

	afterRollback := versionsOf(t, pool, skus...)

	// --- PRESENT 側: 同じことをして中断せずコミットする ---
	err = f.work.Within(ctx, func(ctx context.Context, r application.Repos) error {
		items, err := r.Stock().LoadMany(ctx, domainSKUs)
		if err != nil {
			return err
		}
		for _, item := range items {
			if err := item.Reserve(ref, mustQty(t, 1), 0); err != nil {
				return err
			}
		}
		return r.Stock().Save(ctx, items...)
	})
	require.NoError(t, err, "マルチ SKU のコミット")

	afterCommit := versionsOf(t, pool, skus...)

	for _, sku := range skus {
		t.Logf("観測: SKU=%s 版 before=%d rollback後=%d commit後=%d",
			sku, before[sku], afterRollback[sku], afterCommit[sku])
	}

	// 対を 1 回の観測で突き合わせる。
	for _, sku := range skus {
		assert.Equal(t, before[sku], afterRollback[sku],
			"EMPTY: ロールバック時は版が据え置き（SKU=%s）", sku)
		assert.Equal(t, before[sku]+1, afterCommit[sku],
			"PRESENT: コミット時は版が 1 つ進む（SKU=%s）", sku)
	}
	// 「全対象行が**同一版**」であることも見る。行ごとに確定する実装なら割れうる。
	assert.Equal(t, afterCommit[skus[0]], afterCommit[skus[1]], "PRESENT: 全対象行が同一版")
	assert.Equal(t, afterCommit[skus[1]], afterCommit[skus[2]], "PRESENT: 全対象行が同一版")
}
