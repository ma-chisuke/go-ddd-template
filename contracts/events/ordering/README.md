# 注文コンテキストが発行するメッセージ契約

注文コンテキストが送信し、在庫コンテキストが購読する 2 種類のメッセージの契約です。
封筒（envelope）の仕様とバージョニング方針は種別レベルの話なので [../README.md](../README.md)
にあります。ここはこのコンテキストが**何を発行するか**の一覧です。

| 契約 | `type`（契約識別子） | 何を伝えるか |
|------|---------------------|-------------|
| [`confirm_reservation.schema.json`](confirm_reservation.schema.json) | `ordering.reservation.confirm_requested` | **予約確定コマンド**（`ConfirmReservation`）。注文が durable（確定保存）になったことを受けて、在庫の仮予約（pending）を確定（confirmed）へ進めるよう依頼します |
| [`order_cancelled.schema.json`](order_cancelled.schema.json) | `ordering.order.cancelled` | **注文取消イベント**（`OrderCancelled`）。注文が取り消されたことを通知します。在庫はこれを購読して予約を解放（release）します |

`<name>.baseline.schema.json` は「最後にリリースした契約」のスナップショットで、
後方互換ゲート（`contracts/events/check-compat.sh`）の比較基準です。手で編集するのは
リリース作業のときだけです。

## ディレクトリ名と `type` の接頭辞

このディレクトリ名 `ordering` は**発行元コンテキスト**です（購読側ではありません）。
同じ情報が `type` の const の接頭辞にも書かれているので、両者が一致することを
`contracts/events/check-compat.sh` が機械検査します。二重に持っている情報を検査で
縛らなければ、この分割は平坦だった頃より弱くなります
（`events/inventory/` に `type: ordering.*` のファイルがあっても誰も気づかない）。

送信は注文の `internal/application/messages.go`、受信は在庫の
`internal/application/subscriber.go` です。経路の全体像は
[docs/context-map.md](../../../docs/context-map.md) を参照してください。
