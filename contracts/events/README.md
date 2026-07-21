# クロスコンテキストメッセージ契約（events）

このディレクトリは、境界づけられたコンテキストをまたいで運ばれる **メッセージの契約**
（コマンドとイベント）を、実装から独立した唯一の真実（source of truth）として定義します。
注文コンテキストが送信し、在庫コンテキストが購読する 2 種類のメッセージを収めています。

- `confirm_reservation.schema.json` — **予約確定コマンド**（`ConfirmReservation`）。
  注文が durable（確定保存）になったことを受けて、在庫の仮予約（pending）を確定
  （confirmed）へ進めるよう依頼するクロスコンテキストコマンドです。
- `order_cancelled.schema.json` — **注文取消イベント**（`OrderCancelled`）。
  注文が取り消されたことを通知するドメインイベントで、在庫コンテキストはこれを購読して
  予約を解放（release）します。

## メッセージ封筒（envelope）と payload

いずれのメッセージも、共通の **封筒** に載って運ばれます。封筒は以下の中立なフィールドを
持ちます（トランスポートに依存しない形）。

| フィールド | 説明 |
|-----------|------|
| `id` | メッセージの一意な識別子（受信側の重複排除に使う） |
| `type` | `message_type`。送信先の Consumer を選択する種別 |
| `payload` | **翻訳済み契約**のシリアライズ（JSON 文字列）。コンテキスト間で Go の共有型は渡さない |
| `trace_id` | seam を跨ぐ相関 ID。フロー全体をサービス横断で追跡できるようにする |
| `occurred_at` | メッセージの発生時刻（UTC） |

`payload` は文字列であり、その中身は各スキーマの `$defs/Payload` が定義する JSON を
シリアライズしたものです。相手コンテキストは自分の Go 型ではなく、この翻訳済み契約
（不透明な `reservation_ref` など）だけをデコードします。相手側の内部識別子（`OrderID`
など）は、送信側の腐敗防止層（anti-corruption layer）が `reservation_ref` へ翻訳済みです。

## バージョニング

`type` の文字列（例 `ordering.reservation.confirm_requested`）が契約の識別子です。
破壊的変更を行う場合は、後方互換性を壊さないよう `type` を新設（バージョン付与）し、
受信側が新旧双方の Consumer を登録できるようにしてから移行します。
