#!/usr/bin/env bash
# カバレッジゲート: 各コンテキストの「ドメイン層 + アプリケーション層」の行カバレッジが
# 閾値（既定 80%）以上であることを検証する。CI のマージ前ゲートで実行し、ローカルでも
# 同じスクリプトで再現できる。
#
# 対象を domain + application に限定するのは、業務ルール（domain）とオーケストレーション
# （application）が手書きの中核だからである。生成コード（ogen / sqlc）やアダプタの結線は
# この閾値の対象にしない（team 方針: domain + application を >= 80%）。
set -euo pipefail

threshold="${COVERAGE_MIN:-80}"
repo_root="$(cd "$(dirname "$0")/.." && pwd)"
modules=("contexts/inventory" "contexts/ordering")

status=0
for m in "${modules[@]}"; do
  echo "== カバレッジ: $m （domain + application, 閾値 ${threshold}%） =="
  if (
    cd "$repo_root/$m"
    go test -covermode=atomic -coverprofile=cover.out ./internal/domain/... ./internal/application/... >/dev/null
    pct=$(go tool cover -func=cover.out | awk '/^total:/ {gsub("%","",$3); print $3}')
    rm -f cover.out
    echo "  total: ${pct}%"
    awk -v p="$pct" -v t="$threshold" 'BEGIN { exit !(p+0 >= t+0) }'
  ); then
    :
  else
    echo "::error::カバレッジが閾値 ${threshold}% を下回りました: $m"
    status=1
  fi
done

if [ "$status" -eq 0 ]; then
  echo "カバレッジゲート: OK（全モジュールが閾値 ${threshold}% 以上）"
fi
exit "$status"
