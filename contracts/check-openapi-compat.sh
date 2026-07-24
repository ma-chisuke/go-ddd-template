#!/usr/bin/env bash
# 公開／内部 OpenAPI 契約の後方互換ゲート（oasdiff）。
#
# 各 OpenAPI 契約 <spec>.yaml を、リリース済みベースライン <spec>.baseline.yaml と比較し、
# 消費側（クライアント）を壊す破壊的変更があれば失敗する（--fail-on ERR）。CI のマージ前
# ゲートとして実行し、ローカルでも同じスクリプトで再現できる。
#
# ベースライン戦略:
#   <spec>.baseline.yaml は「最後にリリースした契約」のスナップショットである。リリース
#   （git タグ + GitHub Release、SemVer）のたびに、その時点の <spec>.yaml で対応する
#   <spec>.baseline.yaml を更新して同じコミットに含める。これにより「前回リリース以降の
#   破壊的変更」を PR 単位で検出できる。破壊的変更が必要なときは、メジャーバージョンを上げ、
#   ベースライン更新を意図的なリリース作業として行う。
#
# 前提ツール: oasdiff。版は tools/versions.env（OASDIFF_VERSION）を単一情報源とする
# （ここには版番号をハードコードしない）。
set -euo pipefail
cd "$(dirname "$0")"

# 版の単一情報源を読み込む（このスクリプトは contracts/ にいるためリポジトリ直下は ../）。
set -a && . ../tools/versions.env && set +a

# 互換性を守る対象の契約一覧（公開 2 本 + 在庫の内部 API 1 本）。
specs=(
  "inventory/openapi.yaml"
  "inventory/internal.openapi.yaml"
  "ordering/openapi.yaml"
)

if ! command -v oasdiff >/dev/null 2>&1; then
  echo "::error::oasdiff が見つかりません。'go install github.com/oasdiff/oasdiff@${OASDIFF_VERSION}' を実行してください。"
  exit 1
fi

status=0
for spec in "${specs[@]}"; do
  dir=$(dirname "$spec")
  file=$(basename "$spec")
  base="$dir/${file%.yaml}.baseline.yaml"
  if [ ! -f "$base" ]; then
    echo "  新規契約（ベースラインなし = 非破壊として許可）: $spec"
    continue
  fi
  echo "== 後方互換チェック: $spec （基準: $base） =="
  if ! oasdiff breaking "$base" "$spec" --fail-on ERR; then
    echo "::error::後方互換を壊す API 変更を検出しました: $spec"
    echo "  破壊的変更が必要な場合はメジャーバージョンを上げ、リリース作業として $base を更新してください。"
    status=1
  fi
done

if [ "$status" -eq 0 ]; then
  echo "OpenAPI 契約の後方互換: OK"
fi
exit "$status"
