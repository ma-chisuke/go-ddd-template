package application

import (
	"errors"

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

// ErrOrderNotConfirmedForShipment は、確定済みでない注文に対して出荷を準備しようとした
// ことを表す番兵。
//
// **これがドメイン層ではなくアプリケーション層に在るのは、2 つの集約に跨る事前条件だから
// である。**「注文が確定済みか」は出荷（Shipment）の不変条件ではない — 不変条件なら
// 集約自身が守るべきで、そのためには注文の実体を保持することになり「集約間は識別子で
// 参照する」を壊す。事前条件は、集約をまたいで調整するアプリケーション層が、
// トランザクションの外で読んだ注文に対して確かめる。
//
// port には置かない。コンテキスト境界を跨がない、注文コンテキスト内部の失敗だからである。
var ErrOrderNotConfirmedForShipment = errors.New("注文が確定状態ではないため出荷を準備できません")
