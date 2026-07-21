package port

import "errors"

// このファイルは、腐敗防止層（ACL）ポートが公開する「エラーの語彙」を収める。
// ポートのインターフェース（StockReserver）自体は internal/application に留めるが、
// モジュール外のアダプタ（例: cmd/dev の in-process ACL）が失敗を注文コンテキスト自身の
// 番兵へ翻訳できるよう、番兵だけは公開型として port に置く。application 層はこれらを
// 別名（alias）で参照するため、値としての同一性は保たれ、errors.Is はどちらの名前でも一致する。

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
// ACL アダプタは不達系の失敗をこの番兵と ErrReservationRejected の双方に一致するよう翻訳する
// （errors.Join）。これにより、ErrReservationRejected だけを見るコードでも「予約は成立
// しなかった」と正しく扱え、HTTP マッパは ErrReservationUnavailable を先に判定して 503 を返す。
var ErrReservationUnavailable = errors.New("在庫サービスが利用できません")
