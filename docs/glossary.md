# 用語集の索引と、境界を跨いで同名の語

## 1. 索引

ユビキタス言語は**境界の内側でのみ通用する**というのが DDD の前提です。「同じ語」でも、
話し手が属する境界が違えば別のものを指します。だからこのリポジトリは用語集を 1 つに
まとめず、**境界づけられたコンテキストごとに所有**させています（コンテキストを切り出して
持ち出すとき、用語集もその境界と一緒に付いてきます）。

用語の定義本体は次の 2 つにあり、この文書は**索引と対比だけ**を持ちます（二重管理を作らない）。

- [../contexts/inventory/GLOSSARY.md](../contexts/inventory/GLOSSARY.md) — 在庫（Inventory）の語彙
- [../contexts/ordering/GLOSSARY.md](../contexts/ordering/GLOSSARY.md) — 受注（Ordering）の語彙

なぜこの 2 つに割ったのかは [why-these-boundaries.md](./why-these-boundaries.md) を、
DDD のパターンがどのファイルに実装されているかは [ddd-patterns.md](./ddd-patterns.md) を
参照してください。

## 2. 境界を跨いで同名の語

次の 3 語は**両方の境界に同じ名前で存在し、しかも別の型・別の意味**です。このリポジトリで
最も教材価値の高い箇所であり、境界を引いた根拠そのものでもあります。

| 語 | Ordering での意味と型 | Inventory での意味と型 | なぜ共有しないのか |
| --- | --- | --- | --- |
| `SKU` | 注文明細が**参照する**商品識別子（`order.SKU`）。注文は在庫の実体を知らない | 在庫項目**そのものの同一性**（`inventory.SKU`）。集約の識別子 | 一方は参照、他方は同一性。同じ型にすると、在庫の識別規則がそのまま注文側の制約になる |
| `Quantity` | 注文する数量（`order.Quantity`）。**1 以上**で、加減算を持たない（`Int()` のみ） | 在庫数量（`inventory.Quantity`）。**0 以上**で、`Add` / `Sub`（不足で失敗）/ `GreaterThan` を持つ | 在庫にしかない演算を注文側が持つと、注文が在庫の計算規則に縛られる。値域（1 以上 / 0 以上）も別のドメイン規則から来ている |
| `ReservationRef` | 注文 ID から**決定的に導出**する（`order.ReservationRef` / `DeriveReservationRef`） | **外から受け取る**予約の識別子（`inventory.ReservationRef`）。導出規則を持たない | 導出規則は注文側の関心。在庫が同じ型を持つと、在庫が注文 ID の構造を知ることになる |

**取り違えは Go のコンパイラが防いでいます。** `order.SKU` と `inventory.SKU` は別パッケージの
別の型なので、片方をもう片方の関数へ渡すとビルドエラーになります。境界を跨いで値を渡すときは、
内部のドメイン値オブジェクトではなく**翻訳済みの公開型**（`contexts/<ctx>/port/` の DTO や
`contracts/events/` のメッセージ契約）を使います。翻訳を担うのが腐敗防止層（ACL）です
（[context-map.md](./context-map.md)）。

> **同名だが「機構」であり、語彙の衝突ではないもの**: `DomainEvent` / `Rule` / `FieldViolation`
> と、一部の番兵（`ErrInvalidSKU` / `ErrInvalidQuantity` / `ErrInvalidReservationRef`）も両方の
> 境界に同名で存在しますが、これらは形も意味も同じ**技術的な機構**です。上の 3 語（値オブジェクト）の
> ような「意味の衝突」ではありません。それでも共有型に切り出さないのは、ドメイン層を純粋に保ち、
> コンテキストを単独で切り出せる状態を維持するためです（機構の共有は `shared/` が担い、そこには
> ドメイン語彙を置きません）。
