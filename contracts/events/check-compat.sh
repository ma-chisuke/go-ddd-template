#!/usr/bin/env bash
# クロスコンテキストのメッセージ契約（イベント／コマンドの JSON スキーマ）のゲート。
#
# 3 つの検査を持つ。契約に関する検査を 1 ファイルに凝集させ、make contracts で一度に走らせる。
#
#   1. 発行元の一致 — <発行元ctx>/<name>.schema.json の type.const が "<発行元ctx>." で始まる。
#      発行元は「ディレクトリ名」と「type の const」の 2 箇所に書かれる。二重に持っている
#      情報を検査で縛らなければ、この分割は平坦だった頃より弱くなる
#      （events/inventory/ に type: ordering.* のファイルがあっても誰も気づかない）。
#   2. $id の一致 — $id の末尾が live のリポジトリ相対パスであること。ベースラインの $id は
#      自分自身のファイル名ではなく **live のパス**を名乗る（ベースラインは「その契約の
#      スナップショット」であり、契約の同一性は live のパスで表される）。$id は下の
#      後方互換 extract の対象外なので、この検査が無いと追随を忘れても何も落ちない。
#   3. 後方互換（golden-snapshot） — 各 <name>.schema.json を、同一ディレクトリの
#      リリース済みベースライン <name>.baseline.schema.json と比較し、契約上重要な不変項目が
#      変わっていれば失敗する。
#        - type（message_type = 契約識別子。const 値）
#        - トップレベルの required（封筒の必須フィールド）
#        - $defs/Payload の required（payload の必須フィールド）
#
# スキーマの探索は再帰である（第 2 階層 = 発行元コンテキスト）。検出が 0 件のときは必ず
# 失敗する —— 平坦グロブのままサブディレクトリ化すると 0 件ループで黙って緑になる、という
# fail-open を塞ぐためで、何も検査しなかったことを成功と呼ばない。
#
# バージョニング方針（README / contracts/events/README.md と整合）:
#   破壊的変更は、既存の type/required を「その場で」変えるのではなく、新しい type
#   （= 新しいスキーマファイル）を追加してバージョン移行する。受信側は新旧双方の Consumer を
#   登録できる。したがって既存スキーマの type.const と required は不変であることを要求する。
#   ベースラインに無い新規スキーマ（新しい type）の追加は非破壊として許可する。
#
# リリース（git タグ）のたびに、その時点の <name>.schema.json で <name>.baseline.schema.json を更新する。
#
# 出力には [events] を接頭辞として付ける。OpenAPI 契約側のゲート
# （contracts/api/check-compat.sh）と同名なので、make contracts の出力でどちらが落ちたかを
# 区別できるようにするためである。
#
# 前提ツール: jq。
set -euo pipefail
cd "$(dirname "$0")"

if ! command -v jq >/dev/null 2>&1; then
  echo "::error::[events] jq が見つかりません。イベントスキーマの互換チェックには jq が必要です。"
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

# live スキーマを再帰的に探す（第 2 階層 = 発行元コンテキスト）。
schemas=$(find . -type f -name '*.schema.json' ! -name '*.baseline.schema.json' \
  | sed 's|^\./||' | sort)

if [ -z "$schemas" ]; then
  echo "::error::[events] メッセージ契約のスキーマが 0 件です（探索: **/*.schema.json）。"
  echo "[events] 何も検査しなかったことを成功と呼ばないため、失敗として扱います。"
  exit 1
fi

status=0
while IFS= read -r schema; do
  dir=$(dirname "$schema")
  ctx=$(basename "$dir")
  name=$(basename "$schema")
  base="$dir/${name%.schema.json}.baseline.schema.json"

  # --- 1. 発行元の一致 ------------------------------------------------------
  type=$(jq -r '.properties.type.const' "$schema")
  case "$type" in
    "$ctx".*)
      echo "[events] 発行元の一致: $schema （type = $type）"
      ;;
    *)
      echo "::error::[events] 発行元（ディレクトリ名）と type の接頭辞が一致しません: $schema"
      echo "[events]   期待: type が \"$ctx.\" で始まること"
      echo "[events]   実際: type = $type"
      echo "[events]   events/ の第 2 階層は**発行元コンテキスト**です。置き場所か type のどちらかが誤っています。"
      status=1
      ;;
  esac

  # --- 2. $id の一致 --------------------------------------------------------
  # live も baseline も、期待値は **live のリポジトリ相対パス**である。
  live_rel="contracts/events/$ctx/$name"
  for f in "$schema" "$base"; do
    [ -f "$f" ] || continue
    id=$(jq -r '."$id"' "$f")
    case "$id" in
      *"/$live_rel")
        ;;
      *)
        echo "::error::[events] \$id がファイルの置き場所と一致しません: $f"
        echo "[events]   期待: \$id が \"/$live_rel\" で終わること"
        echo "[events]   実際: \$id = $id"
        if [ "$f" != "$schema" ]; then
          echo "[events]   （ベースラインの \$id は自分のファイル名ではなく live のパスを名乗ります）"
        fi
        status=1
        ;;
    esac
  done

  # --- 3. 後方互換 ----------------------------------------------------------
  if [ ! -f "$base" ]; then
    echo "[events] 新規メッセージ契約（ベースラインなし = 非破壊として許可）: $schema"
    continue
  fi
  cur=$(extract "$schema")
  old=$(extract "$base")
  if [ "$cur" != "$old" ]; then
    echo "::error::[events] 後方互換を壊すメッセージ契約の変更を検出しました: $schema"
    echo "[events]   基準: $old"
    echo "[events]   現在: $cur"
    echo "[events]   破壊的変更は既存の type/required を変えず、新しい type（新しいスキーマファイル）を追加してください。"
    status=1
  else
    echo "[events] 後方互換チェック: $schema （基準: $base）"
  fi
done <<< "$schemas"

if [ "$status" -eq 0 ]; then
  echo "[events] OK（メッセージ契約の配置と後方互換を維持）"
fi
exit "$status"
