#!/usr/bin/env bash
# クロスコンテキストのメッセージ契約（イベント／コマンドの JSON スキーマ）の後方互換ゲート。
#
# 方式（golden-snapshot）: 各 <name>.schema.json を、リリース済みベースライン
# <name>.baseline.schema.json と比較し、契約上重要な不変項目が変わっていれば失敗する。
#   - type（message_type = 契約識別子。const 値）
#   - トップレベルの required（封筒の必須フィールド）
#   - $defs/Payload の required（payload の必須フィールド）
#
# バージョニング方針（README / contracts/events/README.md と整合）:
#   破壊的変更は、既存の type/required を「その場で」変えるのではなく、新しい type
#   （= 新しいスキーマファイル）を追加してバージョン移行する。受信側は新旧双方の Consumer を
#   登録できる。したがって既存スキーマの type.const と required は不変であることを要求する。
#   ベースラインに無い新規スキーマ（新しい type）の追加は非破壊として許可する。
#
# リリース（git タグ）のたびに、その時点の <name>.schema.json で <name>.baseline.schema.json を更新する。
#
# 前提ツール: jq。
set -euo pipefail
cd "$(dirname "$0")"

if ! command -v jq >/dev/null 2>&1; then
  echo "::error::jq が見つかりません。イベントスキーマの互換チェックには jq が必要です。"
  exit 1
fi

# 契約上重要な不変項目だけを抽出して正規化する（キー順・配列順を安定化）。
extract() {
  jq -S '{
    type: .properties.type.const,
    required: (.required // [] | sort),
    payload_required: (.["$defs"].Payload.required // [] | sort)
  }' "$1"
}

status=0
for schema in *.schema.json; do
  case "$schema" in
    *.baseline.schema.json) continue ;;
  esac
  base="${schema%.schema.json}.baseline.schema.json"
  if [ ! -f "$base" ]; then
    echo "  新規メッセージ契約（ベースラインなし = 非破壊として許可）: $schema"
    continue
  fi
  cur=$(extract "$schema")
  old=$(extract "$base")
  if [ "$cur" != "$old" ]; then
    echo "::error::後方互換を壊すメッセージ契約の変更を検出しました: $schema"
    echo "  基準: $old"
    echo "  現在: $cur"
    echo "  破壊的変更は既存の type/required を変えず、新しい type（新しいスキーマファイル）を追加してください。"
    status=1
  else
    echo "  OK（契約の互換性を維持）: $schema"
  fi
done

if [ "$status" -eq 0 ]; then
  echo "メッセージ契約の後方互換: OK"
fi
exit "$status"
