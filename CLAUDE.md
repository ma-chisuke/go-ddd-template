# CLAUDE.md

このリポジトリでの作業ガイドは `AGENTS.md` にまとめています。まずそちらを読んでください。
あわせて `README.md`（全体像）と `CONVENTIONS.md`（Go / DDD の規約）を参照してください。

要点だけ再掲します。

- レイヤーは 4 つ。**adapter/inbound（入口＝駆動側）**、**adapter/outbound（出口＝被駆動側）**、
  **application（ユースケース + ポート）**、**domain（純粋なドメイン）**。ポートは application 層に置く。
- 生成コード（ogen: `internal/adapter/inbound/openapi/`、sqlc:
  `internal/adapter/outbound/postgres/sqlcgen/`）は**手で編集しない**。契約や SQL を
  編集して `go generate ./...` で再生成する。
- 業務ルールは**ドメイン層**に置く。ドメイン層は永続化・HTTP・IO・フレームワーク・アダプタに
  依存しない（depguard で強制）。入口(inbound)と出口(outbound)は互いを直接 import しない。
- 書き込みは必ず `UnitOfWork.Within` の内側で行う。トランザクションを
  `context.Context` に載せない。
