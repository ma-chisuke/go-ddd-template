# CLAUDE.md

このリポジトリでの作業ガイドは `AGENTS.md` にまとめています。まずそちらを読んでください。
あわせて `README.md`（全体像）と `CONVENTIONS.md`（Go / DDD の規約）を参照してください。

要点だけ再掲します。

- 生成コード（ogen: `internal/interfaces/openapi/`、sqlc:
  `internal/infrastructure/postgres/sqlcgen/`）は**手で編集しない**。契約や SQL を
  編集して `go generate ./...` で再生成する。
- 業務ルールは**ドメイン層**に置く。ドメイン層は永続化・HTTP・IO・フレームワークに
  依存しない（depguard で強制）。
- 書き込みは必ず `UnitOfWork.Within` の内側で行う。トランザクションを
  `context.Context` に載せない。
