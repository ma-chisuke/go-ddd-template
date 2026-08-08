# クロスコンテキストメッセージ契約（events）

このディレクトリは、境界づけられたコンテキストをまたいで運ばれる **メッセージの契約**
（コマンドとイベント）を、実装から独立した唯一の真実（source of truth）として定義します。

## 構成 — 第 2 階層は「発行元コンテキスト」

```
contracts/events/
├── README.md      … このファイル（封筒とバージョニング＝種別レベルの話）
├── check-compat.sh … 後方互換ゲート＋配置の機械検査
├── ordering/      … 注文が発行するメッセージ（2 本）
└── inventory/     … 在庫が発行するメッセージ（現在 0 本・意図された空）
```

第 2 階層の軸は**発行元**であって購読側ではありません。同じ情報が各スキーマの `type` の
const の接頭辞にも現れるので、`check-compat.sh` が両者の一致を機械検査します
（`events/ordering/*.schema.json` の `type` は `ordering.` で始まらなければ失敗）。
各コンテキストが何を発行するかは、それぞれの README を参照してください。

- [`ordering/README.md`](ordering/README.md) — 予約確定コマンドと注文取消イベント
- [`inventory/README.md`](inventory/README.md) — 現在は発行なし（意図された空）

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

## `$id`

各スキーマの `$id` の末尾は、そのスキーマの**リポジトリ相対パス**です
（例 `https://go-ddd-template.example/contracts/events/ordering/order_cancelled.schema.json`）。
`<name>.baseline.schema.json` の `$id` は自分自身のファイル名ではなく、対応する **live の
パス**を名乗ります —— ベースラインは「その契約のスナップショット」であり、契約の同一性は
live のパスで表されるからです。どちらも `check-compat.sh` が機械検査します。
