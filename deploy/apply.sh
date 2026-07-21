#!/bin/sh
# DB bring-up の適用オーケストレーション（使い捨て init コンテナのエントリポイント）。
#
# 適用順（各コンテキストごとに）:
#   1. スキーマ名前空間の作成（CREATE SCHEMA IF NOT EXISTS）
#   2. 宣言的スキーマの適用（psqldef、当該スキーマに限定・incremental・非破壊）
#   3. 最小権限ロール / GRANT の適用（自スキーマのみ・実行時 superuser なし）
#   4. 本番参照データ（seed、冪等 upsert）
#   5.（dev/test のみ）フィクスチャ（APPLY_FIXTURES=true のとき）
#
# GRANT / seed はテーブルの存在を前提とするため、必ずスキーマ適用の後に行う。スキーマ適用は
# 管理者ロール（PGUSER）で、実行時のサービス接続はここで作るスコープ済みロールで行う。
#
# 環境変数（compose から注入。すべてデモ専用）:
#   PGHOST / PGPORT / PGUSER / PGPASSWORD / PGDATABASE … 管理者接続（schema 適用・ロール作成用）
#   INVENTORY_SVC_PASSWORD / ORDERING_SVC_PASSWORD    … 各サービスロールのパスワード
#   DB_DIR                                            … 各コンテキストの db/ をマウントする親（既定 /db）
#   APPLY_FIXTURES                                    … "true" のとき dev/test fixtures も適用
set -eu

: "${PGHOST:?PGHOST が未設定です}"
: "${PGUSER:?PGUSER が未設定です}"
: "${PGPASSWORD:?PGPASSWORD が未設定です}"
: "${PGDATABASE:?PGDATABASE が未設定です}"
PGPORT="${PGPORT:-5432}"
DB_DIR="${DB_DIR:-/db}"
APPLY_FIXTURES="${APPLY_FIXTURES:-false}"
export PGPASSWORD PGHOST PGPORT PGUSER PGDATABASE

run_psql() {
    psql -v ON_ERROR_STOP=1 -h "$PGHOST" -p "$PGPORT" -U "$PGUSER" -d "$PGDATABASE" "$@"
}

apply_ctx() {
    ctx="$1"
    svc_pw="$2"
    dir="$DB_DIR/$ctx"

    echo "==> [$ctx] 1/4 スキーマ名前空間を作成（無ければ）"
    run_psql -c "CREATE SCHEMA IF NOT EXISTS $ctx AUTHORIZATION \"$PGUSER\";"

    echo "==> [$ctx] 2/4 宣言的スキーマを適用（psqldef・当該スキーマに限定・非破壊）"
    psqldef -h "$PGHOST" -p "$PGPORT" -U "$PGUSER" \
        --config "$dir/sqldef.yml" -f "$dir/schema.sql" "$PGDATABASE"

    echo "==> [$ctx] 3/4 最小権限ロール / GRANT を適用（自スキーマのみ）"
    run_psql -v svc_password="$svc_pw" -f "$dir/roles.sql"

    echo "==> [$ctx] 4/4 本番参照データ（seed）を適用（冪等 upsert）"
    run_psql -f "$dir/seed.sql"

    if [ "$APPLY_FIXTURES" = "true" ]; then
        echo "==> [$ctx] （dev/test）フィクスチャを適用（本番経路には含めない）"
        run_psql -f "$dir/fixtures.sql"
    fi
}

echo "== DB 準備を開始します（schema -> roles -> seed -> (dev)fixtures） =="
apply_ctx inventory "${INVENTORY_SVC_PASSWORD:?INVENTORY_SVC_PASSWORD が未設定です}"
apply_ctx ordering "${ORDERING_SVC_PASSWORD:?ORDERING_SVC_PASSWORD が未設定です}"
echo "== DB 準備が完了しました =="
