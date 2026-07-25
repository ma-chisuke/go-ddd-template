package problem

// 種別（type）URI のサフィックス台帳（規則 R-1）。
//
// 応答の type は「コンテキストごとの problemTypeBase 定数 + ここのサフィックス」で作る。
// テンプレート利用者は各コンテキストの problemTypeBase 1 箇所を自分の名前空間へ書き換える
// （手順は CONVENTIONS.md）。URI は識別子であり、解決可能である必要はない（OOS-5）。
//
// 同じ status でも原因が異なるなら別の種別を与える（規則 R-2）。404 が TypeNotFound
// （経路が無い）と TypeResourceNotFound（経路はあるが対象が無い）に分かれるのがその実例で、
// これが type URI を導入する価値そのものである。クライアントは status ではなく type で
// 分岐できる。
const (
	// TypeValidationError は 400。リクエストが API 契約に適合しない（E1）。
	TypeValidationError = "validation-error"
	// TypeUnsupportedMediaType は 415。Content-Type がサポート外（E1）。
	TypeUnsupportedMediaType = "unsupported-media-type"
	// TypeNotFound は 404。ルーティング不一致 = そのようなエンドポイントは無い（E2）。
	TypeNotFound = "not-found"
	// TypeMethodNotAllowed は 405。エンドポイントはあるがメソッドが許可されていない（E3）。
	TypeMethodNotAllowed = "method-not-allowed"
	// TypeInvalidInput は 422。ドメインの検証規則違反（E4）。
	TypeInvalidInput = "invalid-input"
	// TypeResourceNotFound は 404。エンドポイントはあるが対象リソースが存在しない（E4）。
	TypeResourceNotFound = "resource-not-found"
	// TypeConflict は 409。現在の状態と矛盾する操作（E4）。
	TypeConflict = "conflict"
	// TypeReservationRejected は 409。在庫予約の拒否（E4。注文コンテキストのみ）。
	TypeReservationRejected = "reservation-rejected"
	// TypeServiceUnavailable は 503。依存サービス不達（E4。注文コンテキストのみ）。
	TypeServiceUnavailable = "service-unavailable"
	// TypeInternalError は 500。予期しないエラー（全経路）。
	TypeInternalError = "internal-error"
)

// detail の定型文（規則 R-12）。
//
// detail に err.Error() を載せてはならない（規則 R-11）。ogen / Go 由来の文言・型名・
// 受信値が外へ漏れるためである。経路ごとの定型文をここに集約し、経路が増えたら 1 行足す。
const (
	DetailValidationError  = "リクエストが API 契約に適合していません"
	DetailUnsupportedMedia = "サポートされていないメディアタイプです"
	DetailNotFound         = "要求されたエンドポイントは存在しません"
	DetailMethodNotAllowed = "このエンドポイントでは許可されていないメソッドです"
	DetailResourceNotFound = "要求されたリソースは存在しません"
	DetailConflict         = "現在の状態ではこの操作を実行できません"
	DetailInvalidInput     = "入力値がドメインの規則を満たしていません"
	// DetailInternalError は 5xx 全般に使う（既存の NewError の文言を維持する）。
	DetailInternalError = "予期しないエラーが発生しました"
)

// titles は type サフィックスに対応する title（規則 R-3）。
//
// title は type と 1 対 1 に対応させ、title から type を逆引きできる状態を保つ。したがって
// status を共有する 2 つの種別には別の title を与える（404 の Not Found と Resource Not
// Found、409 の Conflict と Reservation Rejected）。RFC 9457 の title は「その問題種別の
// 短い要約」であり、発生ごとに変わらないものなので、status の理由句をそのまま使うよりも
// 種別ごとに固有の名前を与えるほうが仕様の意図に沿う。
var titles = map[string]string{
	TypeValidationError:      "Bad Request",
	TypeUnsupportedMediaType: "Unsupported Media Type",
	TypeNotFound:             "Not Found",
	TypeMethodNotAllowed:     "Method Not Allowed",
	TypeInvalidInput:         "Unprocessable Entity",
	TypeResourceNotFound:     "Resource Not Found",
	TypeConflict:             "Conflict",
	TypeReservationRejected:  "Reservation Rejected",
	TypeServiceUnavailable:   "Service Unavailable",
	TypeInternalError:        "Internal Server Error",
}

// TitleOf は type サフィックスに対応する title を返す。台帳に無いサフィックスでは
// fallback（呼び出し側が持つ HTTP の理由句）を返す — 誤った title を出すより安全である。
func TitleOf(suffix, fallback string) string {
	if t, ok := titles[suffix]; ok {
		return t
	}
	return fallback
}
