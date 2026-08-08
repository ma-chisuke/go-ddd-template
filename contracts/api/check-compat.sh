#!/usr/bin/env bash
# 公開／内部 OpenAPI 契約の後方互換ゲート（oasdiff）。
#
# 各 OpenAPI 契約 <spec>.yaml を、リリース済みベースライン <spec>.baseline.yaml と比較し、
# 消費側（クライアント）を壊す破壊的変更があれば失敗する（--fail-on ERR）。CI のマージ前
# ゲートとして実行し、ローカルでも同じスクリプトで再現できる。
#
# 守る対象の決め方（ハイブリッド）:
#   「宣言」（protected.txt の列挙）と「実体」（このディレクトリの自動探索）を突き合わせ、
#   差分があれば失敗する。宣言だけあって実体が無ければ移設漏れかパス誤り、実体だけあって
#   宣言が無ければ「守る」と言い忘れた新しい契約である。列挙だけだと、契約ファイルが
#   移動・改名されても列挙が古いまま「対象が見つからない = 何も検査しない」で緑になり、
#   探索だけだと、意図せず置かれた yaml まで契約として扱ってしまう。
#   検出が 0 件のときは必ず失敗する（何も検査しなかったことを成功と呼ばない）。
#
# ベースライン戦略:
#   <spec>.baseline.yaml は「最後にリリースした契約」のスナップショットである。リリース
#   （git タグ + GitHub Release、SemVer）のたびに、その時点の <spec>.yaml で対応する
#   <spec>.baseline.yaml を更新して同じコミットに含める。これにより「前回リリース以降の
#   破壊的変更」を PR 単位で検出できる。破壊的変更が必要なときは、メジャーバージョンを上げ、
#   ベースライン更新を意図的なリリース作業として行う。
#
# 出力には [api] を接頭辞として付ける。メッセージ契約側のゲート
# （contracts/events/check-compat.sh）と同名なので、make contracts の出力でどちらが
# 落ちたかを区別できるようにするためである。
#
# 前提ツール: oasdiff。版は tools/versions.env（OASDIFF_VERSION）を単一情報源とする
# （ここには版番号をハードコードしない）。
set -euo pipefail
cd "$(dirname "$0")"

# 版の単一情報源を読み込む（このスクリプトは contracts/api/ にいるためリポジトリ直下は ../../）。
set -a && . ../../tools/versions.env && set +a

# --- 宣言と実体の突き合わせ -------------------------------------------------

# 宣言: protected.txt の非コメント非空行。
declared=$(sed -e 's/#.*//' -e 's/[[:space:]]*$//' protected.txt | grep -v '^$' | sort -u || true)

# 実体: このディレクトリ配下の OpenAPI 契約。ベースライン（比較の基準）と ogen の生成設定
# （契約ではない）を除く。*.ogen.yaml の除外は必須である —— 生成設定は隠しファイルを
# やめて <対象の契約名>.ogen.yaml という普通の名前になったため、除外しないと契約として
# 拾われ、oasdiff に契約でないものを食わせることになる。
discovered=$(find . -type f -name '*.yaml' ! -name '*.baseline.yaml' ! -name '*.ogen.yaml' \
  | sed 's|^\./||' | sort -u)

if [ -z "$declared" ]; then
  echo "::error::[api] 守る対象が 0 件です（protected.txt に有効な行がありません）。"
  echo "[api] 何も検査しなかったことを成功と呼ばないため、失敗として扱います。"
  exit 1
fi

if ! diff_out=$(diff <(printf '%s\n' "$declared") <(printf '%s\n' "$discovered")); then
  echo "::error::[api] 宣言（protected.txt）と実体（自動探索）が食い違っています。"
  echo "[api]   '<' = 宣言にあるが実体が無い（移設漏れ／パス誤り）"
  echo "[api]   '>' = 実体があるが宣言が無い（守ると宣言し忘れた契約）"
  printf '%s\n' "$diff_out" | sed 's/^/[api]   /'
  exit 1
fi

if ! command -v oasdiff >/dev/null 2>&1; then
  echo "::error::[api] oasdiff が見つかりません。'go install github.com/oasdiff/oasdiff@${OASDIFF_VERSION}' を実行してください。"
  exit 1
fi

# --- 後方互換の検査 ---------------------------------------------------------

status=0
while IFS= read -r spec; do
  dir=$(dirname "$spec")
  file=$(basename "$spec")
  base="$dir/${file%.yaml}.baseline.yaml"
  if [ ! -f "$base" ]; then
    echo "[api] 新規契約（ベースラインなし = 非破壊として許可）: $spec"
    continue
  fi
  echo "[api] 後方互換チェック: $spec （基準: $base）"
  # oasdiff の標準入力は /dev/null に向ける（この while は宣言リストを標準入力から
  # 読んでいるので、ループ内のコマンドが読み進めてしまわないようにする）。
  if ! oasdiff breaking "$base" "$spec" --fail-on ERR </dev/null; then
    echo "::error::[api] 後方互換を壊す API 変更を検出しました: $spec"
    echo "[api]   破壊的変更が必要な場合はメジャーバージョンを上げ、リリース作業として $base を更新してください。"
    status=1
  fi
done <<< "$declared"

if [ "$status" -eq 0 ]; then
  echo "[api] OK（OpenAPI 契約の後方互換を維持）"
fi
exit "$status"
