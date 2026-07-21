package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"testing"
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
	if err != nil {
		t.Fatalf("ハーネスの構築に失敗しました: %v", err)
	}

	res, err := runScenario(context.Background(), log, h)
	if err != nil {
		t.Fatalf("シナリオの実行に失敗しました: %v", err)
	}

	if res.orderID == "" {
		t.Error("作成された注文 ID が空です")
	}
	if res.placedStatus != "confirmed" {
		t.Errorf("作成後の注文状態: got %q, want %q", res.placedStatus, "confirmed")
	}
	// (a)+(b): 予約 3 が確定まで進み、在庫の reserved に反映されている。
	if res.reservedAfterPlace != demoOrderQty {
		t.Errorf("作成後の引当済み数: got %d, want %d", res.reservedAfterPlace, demoOrderQty)
	}
	if res.cancelledStatus != "cancelled" {
		t.Errorf("取消後の注文状態: got %q, want %q", res.cancelledStatus, "cancelled")
	}
	// (c): 取消イベントで予約が解放され、reserved が 0 に戻っている。
	if res.reservedAfterCancel != 0 {
		t.Errorf("取消後の引当済み数: got %d, want 0", res.reservedAfterCancel)
	}
	// 在庫不足の注文は 409（ErrReservationRejected → Conflict）。
	if res.rejectedStatusCode != http.StatusConflict {
		t.Errorf("在庫不足の注文のステータス: got %d, want %d", res.rejectedStatusCode, http.StatusConflict)
	}
}
