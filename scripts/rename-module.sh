#!/usr/bin/env bash
# このテンプレートを自分のリポジトリの出発点にするための module path 一括置換。
#
# 使い方:
#   scripts/rename-module.sh [--dry-run] <new-module-path>
#   例) scripts/rename-module.sh github.com/acme/shop
#
# 何を置換するのか（1 回の全文置換が 2 つの意味論を同時に処理する）:
#
#   1. **Go の module path** — 各モジュールの go.mod の module 行、すべての import 文
#      （手書き + 生成物）、.golangci.yml の goimports.local-prefixes、そして
#      **depguard の pkg: 指定（15 箇所）**。
#   2. **RFC 9457 problem type URI の名前空間** — エラー応答の "type" に載る
#      https://<module-path>/problems/<種別> という公開契約の値。実装側
#      （各コンテキストの inbound アダプタの problem.go とそのテスト）と、
#      契約側（contracts/**/openapi.yaml と *.baseline.yaml）の**両方**にある。
#
# なぜ専用スクリプトが要るのか（素朴な go mod edit + import 置換では危険な理由）:
#
#   - depguard の deny リストは pkg: "<module-path>/contexts/..." という**完全パス**で
#     層と seam の境界を指定している。ここを取りこぼすと deny が「存在しないパッケージ」を
#     指すようになり、**lint は green のまま、層の純粋性の強制だけが黙って無効化される**。
#   - contracts/**/*.baseline.yaml は「このテンプレートがリリースした契約」のスナップショット
#     であり、後方互換ゲート（oasdiff）の比較対象である。spec と baseline を同時に置換すれば
#     差分ゼロで通り、以後は採用者自身のリリース基準として機能しはじめる。片方だけ置換すると
#     type 値に差が生じてゲートが誤検出する。
#
# 旧 module path は shared/go.mod の module 行から導出する（このスクリプトは旧 path の
# 文字列を持たない）。そのため自己書き換えが構造的に起きず、冪等性も自動的に得られる
# —— 2 回目の実行では旧 = 新となり、その場で「既にリネーム済み」と報告して終了する。
set -euo pipefail

repo_root="$(cd "$(dirname "$0")/.." && pwd)"

usage() {
  cat >&2 <<'USAGE'
使い方: scripts/rename-module.sh [--dry-run] <new-module-path>

  <new-module-path>  新しい module path（例: github.com/acme/shop）
  --dry-run          置換せず、対象ファイルと件数だけを出力する

置換後は次の順で検証してください:
  make build && make test && make contracts
  git add -A && git commit -m "module path を変更"
  make generate-check

  ※ make generate-check は作業ツリー全体を git diff で見るため、リネーム差分を
     未コミットのまま実行すると「生成物が最新ではありません」という誤解を招く
     失敗をします（生成の問題ではありません）。先にコミットしてください。
USAGE
}

# --- 引数 -------------------------------------------------------------------

dry_run=0
new=""

while [ $# -gt 0 ]; do
  case "$1" in
    --dry-run) dry_run=1; shift ;;
    -h|--help) usage; exit 0 ;;
    -*)
      echo "エラー: 不明なオプションです: $1" >&2
      usage
      exit 2
      ;;
    *)
      if [ -n "$new" ]; then
        echo "エラー: module path は 1 つだけ指定してください（余分な引数: $1）" >&2
        usage
        exit 2
      fi
      new="$1"
      shift
      ;;
  esac
done

# --- 入力の検証 -------------------------------------------------------------
# 打ち間違いでリポジトリ全体を壊さないよう、置換を始める前にすべて弾く。

if [ -z "$new" ]; then
  echo "エラー: 新しい module path を指定してください" >&2
  usage
  exit 2
fi
case "$new" in
  *[[:space:]]*)
    echo "エラー: module path に空白を含めることはできません: '$new'" >&2
    usage
    exit 2
    ;;
esac
case "$new" in
  */*) : ;;
  *)
    echo "エラー: module path には '/' が 1 つ以上必要です（例: github.com/acme/shop）: '$new'" >&2
    usage
    exit 2
    ;;
esac
case "$new" in
  */)
    echo "エラー: module path の末尾を '/' にすることはできません: '$new'" >&2
    usage
    exit 2
    ;;
esac

# --- 旧 module path の導出（ハードコードしない） -----------------------------

shared_gomod="$repo_root/shared/go.mod"
if [ ! -f "$shared_gomod" ]; then
  echo "エラー: $shared_gomod が見つかりません（リポジトリのルートから実行してください）" >&2
  exit 1
fi

shared_module="$(sed -n 's/^module[[:space:]][[:space:]]*//p' "$shared_gomod" | head -n 1)"
if [ -z "$shared_module" ]; then
  echo "エラー: shared/go.mod から module 行を読み取れませんでした" >&2
  exit 1
fi
case "$shared_module" in
  */shared) old="${shared_module%/shared}" ;;
  *)
    echo "エラー: shared/go.mod の module が '/shared' で終わっていません: '$shared_module'" >&2
    echo "       このスクリプトは共有モジュールの path から親の module path を導出します。" >&2
    exit 1
    ;;
esac

# --- 冪等: 既にリネーム済みなら何もしない -----------------------------------

if [ "$old" = "$new" ]; then
  echo "既にリネーム済みです（module path は '$new'）。ファイルは変更していません。"
  exit 0
fi

# --- 対象ファイルの列挙 -----------------------------------------------------
# 追跡ファイルだけを対象にする（git grep は .git/ と gitignore 対象を自然に除外する）。
# git が使えない環境（tarball 展開など）向けに grep -r のフォールバックを持つ。

cd "$repo_root"

if command -v git >/dev/null 2>&1 && git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  files="$(git grep -l -F -- "$old" || true)"
else
  echo "注意: git が使えないため grep -r にフォールバックします（.git/ は除外）。" >&2
  files="$(grep -rl -F --exclude-dir=.git -- "$old" . | sed 's|^\./||' || true)"
fi

if [ -z "$files" ]; then
  echo "対象ファイルがありません（'$old' は 1 件も見つかりませんでした）。"
  exit 0
fi

count="$(printf '%s\n' "$files" | wc -l | tr -d ' ')"

# --- --dry-run: 1 バイトも書き込まない --------------------------------------

if [ "$dry_run" -eq 1 ]; then
  echo "dry-run: '$old' → '$new'"
  echo ""
  printf '%s\n' "$files"
  echo ""
  echo "対象 ${count} ファイル（書き込みは行っていません）。"
  exit 0
fi

# --- 置換 -------------------------------------------------------------------
# sed -i は GNU と BSD で構文が非互換なので使わない（新しい前提ツールを増やさない）。
# 一時ファイル + mv で置換する。区切りは '|'（path に '/' を含むため）。
# 旧 path はリテラルであり正規表現のメタ文字を含まない。

echo "置換: '$old' → '$new'（対象 ${count} ファイル）"

printf '%s\n' "$files" | while IFS= read -r f; do
  [ -n "$f" ] || continue
  # 実行ビットなどのパーミッションを引き継ぐため、まず複製してから中身だけを差し替える
  # （既存ファイルへのリダイレクトはモードを変えない）。
  cp "$f" "$f.tmp"
  sed "s|$old|$new|g" "$f" > "$f.tmp"
  mv "$f.tmp" "$f"
done

echo ""
echo "完了: ${count} ファイルを書き換えました。"
echo ""
echo "次に、この順で検証してください:"
echo "  make build            # 全モジュールがビルドできること"
echo "  make test             # 既存テストがすべて通ること"
echo "  make contracts        # 契約（spec と baseline）の後方互換ゲートが通ること"
echo ""
echo "そのうえで、リネーム差分をコミットしてから生成物の冪等性を検証します:"
echo "  git add -A && git commit -m \"module path を ${new} に変更\""
echo "  make generate-check   # 生成物が新しい path で再生成でき、差分が出ないこと"
echo ""
echo "  ※ make generate-check は作業ツリー全体を git diff で見るため、リネーム差分を"
echo "     未コミットのまま実行すると「生成物が最新ではありません」という誤解を招く"
echo "     失敗をします（生成の問題ではありません）。先にコミットしてください。"
