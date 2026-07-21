-- 在庫コンテキストの最小権限ロール（宣言的・冪等）。
--
-- 目的: 実行時に superuser を用いず、在庫サービスが「自分のスキーマ（inventory）だけ」を
-- 読み書きできるロールで接続するようにする（schema-per-context の分離の正しさを、
-- 厳密な per-schema ロール + no-cross-schema-reads で担保する）。
--
-- 適用順（bring-up）: schema（psqldef）→ このロール/GRANT → seed。GRANT は対象スキーマ/
-- テーブルの存在を前提とするため、必ずスキーマ適用の後に実行する。スキーマ適用自体は
-- 管理者ロールで行い、実行時はここで作るスコープ済みロールを使う（二段階の権限）。
--
-- パスワードは SQL に焼き込まず、psql 変数 :svc_password で受け取る（apply スクリプトが
-- 環境変数から渡す）。デモ用の既定値は docker-compose 側にあり、いずれもデモ専用である。

-- ロールが無ければ作成する（PostgreSQL には CREATE ROLE IF NOT EXISTS が無いため \gexec で冪等化）。
-- format(%I,%L) で識別子とリテラルを安全に引用する。
SELECT format('CREATE ROLE %I LOGIN PASSWORD %L', 'inventory_svc', :'svc_password')
WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'inventory_svc')
\gexec

-- 既存ロールでもパスワードを宣言値に合わせる（冪等・再適用で最新化）。
ALTER ROLE inventory_svc LOGIN PASSWORD :'svc_password';

-- 自スキーマの使用権限。他スキーマ（ordering）には一切付与しない（no-cross-schema-reads）。
GRANT USAGE ON SCHEMA inventory TO inventory_svc;

-- 自スキーマの既存テーブルへの DML（DDL は付与しない = マイグレーションは管理者ロールのみ）。
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA inventory TO inventory_svc;

-- 今後スキーマ適用で管理者が作るテーブルにも自動で同じ権限が付くよう既定権限を設定する。
ALTER DEFAULT PRIVILEGES IN SCHEMA inventory
    GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO inventory_svc;

-- 念のため、他コンテキストのスキーマへのアクセスを明示的に剥奪する（存在すれば）。
-- 既定でも USAGE は無いが、意図を明示し二重に守る。
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM information_schema.schemata WHERE schema_name = 'ordering') THEN
        EXECUTE 'REVOKE ALL ON SCHEMA ordering FROM inventory_svc';
    END IF;
END
$$;
