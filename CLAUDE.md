# CLAUDE.md

このリポジトリでの作業ガイドは `AGENTS.md` にまとめています。まずそちらを読んでください。
あわせて `README.md`（全体像）と `CONVENTIONS.md`（Go / DDD の規約）を参照してください。

機械可読な契約（真実の源）は `contracts/`（OpenAPI 公開/内部 + イベントスキーマ）と各
コンテキストの `db/`（SQL）にあります。散文ドキュメントはこれらを**複製せず参照**します。

要点だけ再掲します（詳細と根拠は `AGENTS.md`）。

- レイヤーは 4 つ。**adapter/inbound（入口＝駆動側）**、**adapter/outbound（出口＝被駆動側）**、
  **application（ユースケース + ポート）**、**domain（純粋なドメイン）**。ポートは application 層に置く。
- 生成コード（ogen: `internal/adapter/inbound/openapi/`、sqlc:
  `internal/adapter/outbound/postgres/sqlcgen/`）は**手で編集しない**。契約や SQL を
  編集して `go generate ./...` で再生成する。
- 業務ルールは**ドメイン層**に置く。ドメイン層は永続化・HTTP・IO・フレームワーク・アダプタに
  依存しない（depguard で強制）。入口(inbound)と出口(outbound)は互いを直接 import しない。
- 書き込みは必ず `UnitOfWork.Within` の内側で行う。トランザクションを
  `context.Context` に載せない。
- **コンテキストの seam を跨がない**。注文は在庫の Go パッケージ（`contexts/inventory/**`）を
  import せず、`clients/inventory` 越しに HTTP でのみ到達する。境界を跨ぐのは翻訳済み公開型
  （`port` の DTO、`contracts/events/` のメッセージ）だけ。相手の番兵は自分の番兵へ翻訳する。
- **エラー応答から内部実装を漏らさない**。エラーは `application/problem+json`（RFC 9457）で
  返し、`detail` と `invalid-params[].reason` は本プロジェクトの定型文だけを使う。
  `err.Error()` をそのまま載せない。ogen / Go 由来の文言も、問題となった受信値
  （SKU・数量・在庫数）も含めない。ogen サーバは必ず `NewServer(h, h.ServerOptions()...)` で
  組み立てる（渡し忘れると ogen の既定ハンドラが内部文字列を返す）。ドメインの検証規則は
  `Rule{Field, Code, Err}` に 1 つだけ定義し、呼び出し側は `VQuantity.Violated("...")`
  の 1 行で返す。規則を足すコストは **3 箇所**（domain の `Rule` 1 行 + interfaces の
  `domainReasons` 1 行 + 契約 OpenAPI の `InvalidParam.code` `enum` 1 行、在庫は公開・内部の
  両契約）。`code` は契約で `enum` 化されており、足し忘れると生成型の `Validate()` が弾いて
  CI が落ちる。番兵は消さない（`errors.Is` の判定単位）。公開契約のオペレーションは返しうる
  4xx/5xx を明示ステータス + `default`（未処理 → 500）で宣言し、`handler.go` の戻り型は生成
  される `<Op>Res` union になる（本体は変えない）。内部契約
  `contracts/inventory/internal.openapi.yaml` は `default` のみに保つ（生成クライアント経由の
  ACL がステータス区分だけを見る＝規則 R-16。明示コードは union の値を返し ACL 翻訳を壊す）。
  `type` は `code` と違い OpenAPI の `enum` にしない（`format: uri` のまま。台帳は
  `shared/problem/types.go`）。詳細は
  `CONVENTIONS.md` の「HTTP エラー応答（RFC 9457 / Problem Details）」と `AGENTS.md` のレシピ。
- **秘密情報をハードコードしない**。DB 資格情報・トークンはコード/イメージに焼き込まず、
  実行時に環境変数から注入する（compose の認証情報はデモ専用）。

やってはいけないこと（禁止パターン）: 生成コードの手編集 / アダプタ層への業務ロジック /
秘密のハードコード / 他コンテキストの `internal/` の import / トランザクションを `ctx` に載せる /
エラー応答に `err.Error()` や受信値を載せる / `NewServer` にサーバオプションを渡し忘れる。

## 動かし方（2 モード）

- `go run ./cmd/dev` — Docker 不要。両コンテキストを 1 プロセスで結線して一気に動かす
  （同期 in-process 配送。decoupling は示すが遅延ある結果整合は示さない）。
- `set -a && . ./tools/versions.env && set +a && docker compose up --build` — 分散サービス。
  init コンテナが schema（psqldef）→ ロール/GRANT → seed →（dev）fixtures を適用してから
  2 サービスを起動する。公開 API のみホスト公開。versions.env の export は migrate の psqldef 版を渡すため。
