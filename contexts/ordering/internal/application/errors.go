package application

import (
	"github.com/example/go-ddd-template/contexts/ordering/port"
)

// ErrReservationRejected は、在庫予約が拒否されたことを表す注文コンテキスト自身の番兵。
//
// 在庫不足（在庫側の 409）や在庫サービスの不達（timeout / 5xx）といった ACL の失敗は、
// すべてこの番兵へ翻訳する。決して在庫側の番兵（ErrInsufficientStock など）を alias せず、
// 注文側は自分の語彙だけで失敗を表現する（境界を跨いでエラーを漏らさない）。
//
// 番兵の実体は公開パッケージ port に置き、ここではその別名として参照する（値としての
// 同一性は保たれる）。モジュール外のアダプタ（cmd/dev の in-process ACL）も port.ErrReservationRejected を
// 名指しでき、application 層のコードは短い名前で扱える。
var ErrReservationRejected = port.ErrReservationRejected

// ErrReservationUnavailable は、在庫サービスが不達・タイムアウト・5xx で予約可否を
// 判定できなかったことを表す番兵。ErrReservationRejected とは別に、HTTP マッパが
// 「一時的にサービス利用不可（503）」と「業務的な拒否（409）」を区別できるようにするための
// 注文コンテキスト自身の番兵である。
//
// aclhttp は不達系の失敗をこの番兵と ErrReservationRejected の双方に一致するよう翻訳する
// （errors.Join）。これにより、ErrReservationRejected だけを見るコードでも「予約は成立
// しなかった」と正しく扱え、HTTP マッパは ErrReservationUnavailable を先に判定して 503 を返す。
// 実体は port に置き、ここではその別名として参照する。
var ErrReservationUnavailable = port.ErrReservationUnavailable
