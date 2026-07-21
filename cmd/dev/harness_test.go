package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHarnessScenario は開発ハーネスの端から端までのスモークテスト。
// インメモリ + 同期 in-process publisher により決定的に完結するため、待機やリトライは不要。
//
// 検証する seam の 3 フロー:
//   - (a) 同期予約: 注文作成で在庫を予約 → 成功で Confirmed。
//   - (b) 確定コマンド: ConfirmReservation が在庫へ届き、予約が pending → confirmed（reserved に反映）。
//   - (c) 非同期取消イベント: OrderCancelled が在庫へ届き、予約が解放される（reserved が 0 に戻る）。
//   - 在庫不足の注文は 409 で拒否される（ACL がエラーを注文側の番兵へ翻訳）。
func TestHarnessScenario(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	h, err := newHarness(harnessDeps{logger: log})
	require.NoError(t, err, "ハーネスの構築に失敗しました")

	res, err := runScenario(context.Background(), log, h)
	require.NoError(t, err, "シナリオの実行に失敗しました")

	assert.NotEmpty(t, res.orderID, "作成された注文 ID が空です")
	assert.Equal(t, "confirmed", res.placedStatus, "作成後の注文状態")
	// (a)+(b): 予約 3 が確定まで進み、在庫の reserved に反映されている。
	assert.Equal(t, demoOrderQty, res.reservedAfterPlace, "作成後の引当済み数")
	assert.Equal(t, "cancelled", res.cancelledStatus, "取消後の注文状態")
	// (c): 取消イベントで予約が解放され、reserved が 0 に戻っている。
	assert.Zero(t, res.reservedAfterCancel, "取消後の引当済み数")
	// 在庫不足の注文は 409（ErrReservationRejected → Conflict）。
	assert.Equal(t, http.StatusConflict, res.rejectedStatusCode, "在庫不足の注文のステータス")
}
