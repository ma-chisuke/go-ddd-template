package application

import (
	"context"
	"errors"

	"github.com/example/go-ddd-template/contexts/ordering/port"
)

// StockReserver は在庫コンテキストへの腐敗防止層（ACL）ポート。注文コンテキストは
// このポートを通じてのみ在庫を予約・解放し、在庫のドメイン型を一切知らない
// （翻訳済み DTO port.ReserveLine だけを渡す）。
//
// 重要（境界規則）: これは在庫サービスへの外部同期呼び出し（本番は HTTP）であり、
// トランザクションには載らない。したがって Repos には含めず、ユースケースは UoW の
// 外側でこのポートを呼ぶ（HTTP 呼び出しが DB トランザクションを跨いで保持されるアンチ
// パターンを避けるため）。実装は adapter/outbound 層に置く（本番は生成クライアント
// clients/inventory を用いる aclhttp、開発用は in-process アダプタ）。
type StockReserver interface {
	// Reserve は予約参照 ref に対して lines をまとめて予約する。予約が拒否された
	// （在庫不足）場合は ErrReservationRejected、在庫サービスが不達・タイムアウト・5xx の
	// 場合は ErrReservationUnavailable を返す（いずれも注文コンテキスト自身の番兵へ翻訳し、
	// 在庫側の番兵はそのまま漏らさない）。
	Reserve(ctx context.Context, ref string, lines []port.ReserveLine) error

	// Release は予約参照 ref の予約を解放する（保存失敗時の best-effort な補償解放）。
	// 冪等であり、未知の参照でも安全に呼べる。
	Release(ctx context.Context, ref string) error
}

// ErrReservationRejected は、在庫予約が拒否されたことを表す注文コンテキスト自身の番兵。
//
// 在庫不足（在庫側の 409）や在庫サービスの不達（timeout / 5xx）といった ACL の失敗は、
// すべてこの番兵へ翻訳する。決して在庫側の番兵（ErrInsufficientStock など）を alias せず、
// 注文側は自分の語彙だけで失敗を表現する（境界を跨いでエラーを漏らさない）。
var ErrReservationRejected = errors.New("在庫の予約が拒否されました")

// ErrReservationUnavailable は、在庫サービスが不達・タイムアウト・5xx で予約可否を
// 判定できなかったことを表す番兵。ErrReservationRejected とは別に、HTTP マッパが
// 「一時的にサービス利用不可（503）」と「業務的な拒否（409）」を区別できるようにするための
// 注文コンテキスト自身の番兵である。
//
// aclhttp は不達系の失敗をこの番兵と ErrReservationRejected の双方に一致するよう翻訳する
// （errors.Join）。これにより、ErrReservationRejected だけを見るコードでも「予約は成立
// しなかった」と正しく扱え、HTTP マッパは ErrReservationUnavailable を先に判定して 503 を返す。
var ErrReservationUnavailable = errors.New("在庫サービスが利用できません")
