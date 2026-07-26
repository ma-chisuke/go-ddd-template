#!/usr/bin/env bash
# 規約ゲート: golangci-lint で表現できない規約を機械的に検査する。
# CI のマージ前ゲート（make conventions → make ci）で実行し、ローカルでも同じスクリプトで
# 再現できる。規約の本文は CONVENTIONS.md と docs/testing-conventions.md にあり、
# ここは「その文言のうち機械化できるもの」だけを実装する。
#
# 検査は 10 個（fail 9 個 + warn 1 個）。fail は終了コード 1、warn は報告のみで終了コード 0 のまま:
#   1  テスト関数名の主題の一意性（C-1b）                    fail
#   2  t.Run の 8 語語彙（D-1 / D-2）                        fail
#   2' テーブル駆動の name フィールドの 8 語語彙（D-6）        fail
#   3  t.Run 名の / 不在（D-4）                              fail
#   3' テーブル駆動の name フィールドの / 不在（D-6）          fail
#   4  カタログ的ファイル名の不在（B-4）                      fail
#   5  package 名 = ディレクトリ名（A-7）                     fail
#   6  規約系 Markdown の半角スペース境界（F-3）              fail
#   7  ファイル凝集（40 行未満の単型ファイルの乱立）           warn
#   8  位置指定の複合リテラルの不在（D-6）                     fail
#
# 検査 8 は検査 2' / 3' の**盲点をふさぐためだけに在る**。検査 2' は `name:` に続く文字列
# リテラルを拾うので、`{"空 SKU", …}` のように位置で並べたケースは構造体に name フィールドが
# あっても見えない。つまり検査 2' が 0 件でも「準拠しているから 0」なのか「見えていないから 0」
# なのか区別できない。検査 8 が位置指定そのものを禁じることで、検査 2' の視界が
# テーブルのケース名を悉皆的に覆う。
#
# 実装方針: go/parser を使わず grep + sort + uniq + awk で書く。依存を増やさず、
# テンプレートの読者が読める規模に保つためである。この選択の代償として
# 「機械検査できない領域」が残るので、それは既知の限界として規約に明記してある
# （検査 2 / 3 は fmt.Sprintf 由来のサブテスト名を見られない）。
set -euo pipefail

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$repo_root"

# --- 除外の指定（冒頭 1 箇所に集約する。ルールごとに散らさない） ----------------

# 走査対象のトップレベルディレクトリ。
SCAN_DIRS=(shared clients contexts cmd)

# 検査 1 で対象外にするテスト関数の接頭（Test 以外の 3 種）。
#   Fuzz / Benchmark / Example は C-1 の 2 形の制約を受けない。

# 検査 4（カタログ的ファイル名）の禁止語幹。<語幹>.go と <語幹>_test.go の両方を禁じる。
CATALOG_STEMS=(value_objects types models utils helpers common misc)

# 検査 6（F-3）の対象となる規約系 Markdown。
DOC_TARGETS=(CONVENTIONS.md docs/testing-conventions.md AGENTS.md CLAUDE.md README.md)

# 検査 7 で凝集の判定から外すファイル名（役割が固定されており単型・短くて当然のもの）。
COHESION_EXCLUDE=(doc.go generate.go errors.go)

# 検査 7 の閾値: この行数未満の単型ファイルが、同一パッケージにこの数以上あれば warn。
COHESION_MAX_LINES=40
COHESION_MIN_FILES=3

# 8 語の閉じた語彙（docs/testing-conventions.md の D-2）。
VOCAB='正常系|異常系|境界|冪等|並行|契約|回帰|性質'

# 検査 8（位置指定の複合リテラル）で「位置指定ではない」と見なす形。テーブル以外の複合リテラルを
# 巻き込まないための線引きをここ 1 箇所に集める（awk 側にリテラルを散らさない）。
#   OK 1: フィールド名つきの 1 行要素            {name: "…", want: …}
#   OK 2: 開き括弧だけの行の次にフィールド名が来る  { → name: "…"
# 逆に「位置指定」と断ずるのは、開き括弧の直後（同じ行、または次の行の先頭）に**値リテラル**
# （文字列・生文字列・数値）が来る場合だけに限る。この線引きにより、文で始まる裸ブロック
# `{ x := 1 … }` や map のキー `"a": {…}` を誤検出しない。
# ブラケット式（[{] / [[:blank:]]）で書くのは、awk の -v がバックスラッシュを解釈するため。
POSITIONAL_KEYED_FIELD='^[[:blank:]]*[{][A-Za-z_][A-Za-z0-9_]*:'
POSITIONAL_VALUE_HEAD='^[[:blank:]]*("|`|-?[0-9])'

# テーブル以外の正当な位置指定（[][]string のような入れ子のスライスリテラルなど）が
# どうしても必要になったファイルを、リポジトリルートからの相対パスで列挙する。
# 現時点で該当は無い（空）。足すときは理由をコメントで併記すること。
POSITIONAL_EXCLUDE_FILES=()

# --- 報告 -------------------------------------------------------------------

fail_count=0
warn_count=0

# fail は「FAIL: <検査名>: <位置>: <内容>」の 1 行形式で出す（CI ログで grep できるように）。
report_fail() {
  echo "FAIL: $1: $2"
  fail_count=$((fail_count + 1))
}

report_warn() {
  echo "WARN: $1: $2"
  warn_count=$((warn_count + 1))
}

# 生成ファイルかどうか。Code generated ヘッダを持つものは手で直せないので検査対象外にする
# （sqlc の models.go のようにツールが名前を決めるファイルは B-4 に従わせられない）。
is_generated() {
  head -n 5 "$1" 2>/dev/null | grep -q 'Code generated'
}

# --- 検査 1: テスト関数名の主題の一意性（C-1b） -------------------------------
#
# 同一ディレクトリ内で同じ <主題> を持つ Test 関数が 2 つ以上あるなら、そのすべてが
# _<修飾> を持つこと。裏返すと、修飾なしの Test<主題> はその主題を扱う唯一の関数にだけ許される。
#
# 集計の単位はディレクトリ（= go test が 1 つのテストバイナリとしてリンクする単位）にする。
# <pkg> と <pkg>_test が同居していても同じ単位として数える。宣言された package 名で分けると
# 内部テストと外部テストに同名の主題が分かれて取りこぼしになる。
#
# 関数本体は見ない。C-1b は関数名だけで判定できる（旧規則は t.Run の出現回数を数えていたが、
# テーブル駆動テストが常に 1 回になるため誤判定した。C-1b はその問題を構造的に避けている）。
check_test_subject_uniqueness() {
  local dirs dir subject total
  dirs=$(find "${SCAN_DIRS[@]}" -name '*_test.go' -print0 2>/dev/null | xargs -0 -n1 dirname | sort -u)
  for dir in $dirs; do
    # 「主題 修飾有無」の 2 列を作り、主題ごとに「総数」と「修飾なしの数」を数える。
    #
    # while はプロセス置換で回す。パイプで渡すと while がサブシェルになり、
    # report_fail が増やす fail_count が呼び出し元へ戻らない（= 違反を報告しているのに
    # 終了コードが 0 になる）。この形は「rule はあるが何も検査していない」と同じ帰結になるので、
    # このスクリプト内の while は必ずプロセス置換かヒアストリングで回す。
    while read -r subject total; do
      report_fail "検査 1 テスト関数名の主題の一意性（C-1b）" \
        "$dir: ${subject} が修飾なしで存在するが、同じ主題の関数が ${total} 個ある（すべてに _<修飾> が必要）"
    done < <(grep -hoE '^func Test[A-Za-z0-9]+(_[A-Za-z0-9_]+)?\(' "$dir"/*_test.go 2>/dev/null |
      sed -E 's/^func (Test[A-Za-z0-9]+)(_[A-Za-z0-9_]+)?\($/\1 \2/' |
      awk '{ total[$1]++; if (NF == 1) bare[$1]++ }
           END { for (s in total) if (total[s] >= 2 && bare[s] > 0) print s, total[s] }')
  done
}

# --- 検査 2 / 3: t.Run の 8 語語彙と / 不在（D-1 / D-2 / D-4） ----------------
#
# t.Run("...") の第 1 引数が文字列リテラルのものを抽出する。区切りは半角コロン + 半角スペース 1 つに
# 固定する（全角コロンは許可しない — 表記を 1 つに保つ）。
check_trun_names() {
  local hits
  hits=$(grep -rnoE 't\.Run\("[^"]*"' --include='*_test.go' "${SCAN_DIRS[@]}" 2>/dev/null || true)
  [ -z "$hits" ] && return 0

  while IFS= read -r hit; do
    local loc name
    loc="${hit%%:t.Run(*}"
    name=$(printf '%s' "$hit" | sed -E 's/^.*t\.Run\("(.*)"$/\1/')

    if ! printf '%s' "$name" | grep -qE "^(${VOCAB}): "; then
      report_fail "検査 2 t.Run の 8 語語彙（D-1 / D-2）" "$loc: \"$name\""
    fi
    if printf '%s' "$name" | grep -q '/'; then
      report_fail "検査 3 t.Run 名の / 不在（D-4）" "$loc: \"$name\""
    fi
  done <<< "$hits"
}

# --- 検査 2' / 3': テーブル駆動の name フィールド（D-6） ----------------------
#
# name: に続く文字列リテラルを抽出する。行内のどこにあってもよい（アンカーしない）
# — 1 行に書かれたインライン構造体リテラル {name: "...", ts: ...} を取りこぼさないため。
#
# D-6 がテーブルに name フィールドを required にしているので、この抽出で
# テーブル駆動のケース名を悉皆的に拾える。位置指定の第 1 要素や map のキーを
# サブテスト名にすると機械検査できないため、規約側でそれらを禁じている。
check_table_case_names() {
  local hits
  hits=$(grep -rnoE 'name: *"[^"]*"' --include='*_test.go' "${SCAN_DIRS[@]}" 2>/dev/null || true)
  [ -z "$hits" ] && return 0

  while IFS= read -r hit; do
    local loc name
    loc="${hit%%:name:*}"
    name=$(printf '%s' "$hit" | sed -E 's/^.*name: *"(.*)"$/\1/')

    if ! printf '%s' "$name" | grep -qE "^(${VOCAB}): "; then
      report_fail "検査 2' テーブル駆動 name の 8 語語彙（D-6）" "$loc: \"$name\""
    fi
    if printf '%s' "$name" | grep -q '/'; then
      report_fail "検査 3' テーブル駆動 name の / 不在（D-6）" "$loc: \"$name\""
    fi
  done <<< "$hits"
}

# --- 検査 8: 位置指定の複合リテラルの不在（D-6） ------------------------------
#
# *_test.go の複合リテラルの要素は、必ずフィールド名つきで書く。テーブル駆動のケース名を
# 位置で並べると、検査 2' / 3'（name: に続く文字列を拾う）から見えなくなるためである。
# 検査 2' が 0 件であることを「準拠している」と読めるようにするのが、この検査の役目。
#
# 検出は 2 形:
#   inline    行頭が { で始まり、直後がフィールド名でない  → {"境界: …", 1},
#   multiline 開き括弧だけの行の次が値リテラルで始まる      → {  ⏎  "境界: …",
#
# 既知の限界（規約にも明記）: gofmt が 1 行 1 要素に整形することを前提にしている。
# `[]T{{…}, {…}}` のように 1 行へ詰め込んだ形や、`}, {…},` のように閉じ括弧と同じ行に
# 書いた形は行頭が { にならないので見えない。本番コード（*_test.go 以外）は対象外で、
# import した型の unkeyed リテラルは go vet の composites が別に見ている。
check_positional_case_literals() {
  local f hit loc content ex skip
  while IFS= read -r f; do
    [ -z "$f" ] && continue
    skip=0
    for ex in ${POSITIONAL_EXCLUDE_FILES[@]+"${POSITIONAL_EXCLUDE_FILES[@]}"}; do
      [ "$f" = "$ex" ] && skip=1
    done
    [ "$skip" -eq 1 ] && continue
    while IFS= read -r hit; do
      [ -z "$hit" ] && continue
      loc="$f:${hit%%:*}"
      content=$(printf '%s' "${hit#*:}" | sed -E 's/^[[:space:]]+//')
      report_fail "検査 8 位置指定の複合リテラルの不在（D-6）" \
        "$loc: 位置ではなくフィールド名で書く（ケース名は name: に入れる）: ${content}"
    done < <(awk -v keyed="$POSITIONAL_KEYED_FIELD" -v value_head="$POSITIONAL_VALUE_HEAD" '
      # raw string リテラル（バッククォート）の中身は Go の構文ではないので検査しない。
      # 整形した JSON をテストデータとして埋め込むと「{ の次行が値リテラル」の形に一致して
      # 偽陽性になる（レビュアの指摘で実際に再現した）。行内のバッククォートが奇数個なら
      # 開閉が切り替わる、という素朴な追跡で足りる（1 行に 2 個以上書く形は gofmt が崩さない）。
      inraw == 1 {
        if (gsub(/`/, "&") % 2 == 1) inraw = 0
        pending = 0
        next
      }
      { if (gsub(/`/, "&") % 2 == 1) inraw = 1 }
      # 直前が「開き括弧だけの行」で、この行が値リテラルで始まる = 複数行の位置指定
      pending == 1 {
        pending = 0
        if ($0 ~ value_head) { print FNR ":" $0 }
      }
      /^[[:blank:]]*[{][[:blank:]]*$/ { pending = 1; next }
      # 1 行に書かれた要素。フィールド名で始まらなければ位置指定
      /^[[:blank:]]*[{][^[:blank:]]/ { if ($0 !~ keyed) print FNR ":" $0 }
    ' "$f")
  done < <(find "${SCAN_DIRS[@]}" -type f -name '*_test.go' 2>/dev/null | sort)
}

# --- 検査 4: カタログ的ファイル名の不在（B-4） -------------------------------
#
# 生成物は除外する。sqlc の models.go のようにツールが出力名を決めるファイルは
# 改名できず、CONVENTIONS.md の「生成物は手で編集しない」とも衝突するためである。
check_catalog_filenames() {
  local stem f
  for stem in "${CATALOG_STEMS[@]}"; do
    while IFS= read -r f; do
      [ -z "$f" ] && continue
      is_generated "$f" && continue
      report_fail "検査 4 カタログ的ファイル名の不在（B-4）" \
        "$f: ファイル名は中身の概念を名指しする（禁止語幹: ${stem}）"
    done < <(find "${SCAN_DIRS[@]}" -type f \( -name "${stem}.go" -o -name "${stem}_test.go" \) 2>/dev/null || true)
  done
}

# --- 検査 5: package 名 = ディレクトリ名（A-7） ------------------------------
#
# 生成物と *_test.go を除外する。*_test.go は <pkg>_test という別名が正しいので対象外
# （その規約は testpackage linter が別に強制する）。main は Go の慣習でディレクトリ名と
# 一致しないので除外する。
check_package_matches_dir() {
  local f pkg dir
  while IFS= read -r f; do
    is_generated "$f" && continue
    pkg=$(grep -m1 -oE '^package [A-Za-z0-9_]+' "$f" 2>/dev/null | awk '{print $2}')
    [ -z "$pkg" ] && continue
    [ "$pkg" = "main" ] && continue
    dir=$(basename "$(dirname "$f")")
    if [ "$pkg" != "$dir" ]; then
      report_fail "検査 5 package 名 = ディレクトリ名（A-7）" \
        "$f: package $pkg だがディレクトリは $dir（別名で回避せずディレクトリ名を合わせる）"
    fi
  done < <(find "${SCAN_DIRS[@]}" -type f -name '*.go' ! -name '*_test.go' 2>/dev/null | sort)
}

# --- 検査 6: 規約系 Markdown の半角スペース境界（F-3） -----------------------
#
# 日本語（かな・カナ・漢字）の直後に英数字が空白なしで続く箇所を検出する。
#
# コードフェンス・インラインコードスパン・URL・Markdown リンクの括弧内は除外する。
# コードフェンスは複数行にまたがるので行単位の grep では取れない（この除外が
# 広すぎ／狭すぎでないことはカナリア検証で「本文の 1 件は報告され、フェンス内の同じ文字列は
# 報告されない」を 1 回の観測で確かめる — CONVENTIONS.md の G-2）。
#
# ここだけ perl を使う。理由は移植性である。
#   grep の bracket 範囲 [ぁ-んァ-ヶ一-龠] は **文字単位で解釈される保証がない**。
#   C ロケールの BSD grep（macOS 既定）はこれを「バイトの集合」として扱うため、
#   全角括弧「（」の 3 バイト目が範囲に入って直後の英字と組み合わさり、
#   `# 規約（CONVENTIONS）` のような無害な行を誤検出する（実測で 307 件の偽陽性）。
#   LC_ALL に UTF-8 ロケールを与える回避策は、利用できるロケール名が
#   macOS（en_US.UTF-8）と CI の ubuntu（C.UTF-8）で異なるため当てにできない。
#   perl は -CSD で入出力を UTF-8 に固定でき、\x{} のコードポイント範囲を
#   ロケールに関係なく文字単位で解釈する。macOS と ubuntu-latest の双方に既定で入っている。
check_doc_spacing() {
  local doc line_no content m
  for doc in "${DOC_TARGETS[@]}"; do
    [ -f "$doc" ] || continue
    while IFS= read -r m; do
      line_no="${m%%:*}"
      content="${m#*:}"
      report_fail "検査 6 規約系 Markdown の半角スペース境界（F-3）" \
        "$doc:$line_no: 日本語と英数の間に半角スペース 1 つを入れる: ${content}"
    done < <(perl -CSD -e '
      my $in_fence = 0;
      my $n = 0;
      while (my $line = <STDIN>) {
        $n++;
        chomp $line;
        # コードフェンスの開始／終了行そのものと、その内側は検査しない
        if ($line =~ /^\s*```/) { $in_fence = !$in_fence; next; }
        next if $in_fence;
        my $t = $line;
        $t =~ s/`[^`]*`//g;          # インラインコードスパン
        $t =~ s{https?://[^\s)]*}{}g; # URL
        $t =~ s/\]\([^)]*\)/]/g;      # Markdown リンクの遷移先
        # かな・カナ・漢字の直後に英数字が空白なしで続く境界
        if ($t =~ /[\x{3041}-\x{3093}\x{30A1}-\x{30F6}\x{4E00}-\x{9FA0}][A-Za-z0-9]/) {
          print "$n:$line\n";
        }
      }
    ' < "$doc")
  done
}

# --- 検査 7: ファイル凝集（warn） -------------------------------------------
#
# 「公開型を 1 個だけ含み COHESION_MAX_LINES 行未満」のファイルが同一パッケージに
# COHESION_MIN_FILES 個以上あれば、束ね候補として報告する。
#
# fail にしないのは、これが経験則であって機械的に 0 にできる指標ではないからである
# （標準ライブラリにも build tag 以外の 40 行未満が 456 件ある）。判断を人間に委ねる。
check_file_cohesion() {
  local dir f lines exported small excluded name
  local dirs
  dirs=$(find "${SCAN_DIRS[@]}" -type f -name '*.go' 2>/dev/null | xargs -n1 dirname | sort -u)
  for dir in $dirs; do
    small=""
    for f in "$dir"/*.go; do
      [ -f "$f" ] || continue
      case "$f" in *_test.go) continue ;; esac
      is_generated "$f" && continue
      # build tag 付きファイルは、環境ごとに小さく分かれるのが正しいので対象外。
      grep -qE '^//go:build' "$f" && continue
      name=$(basename "$f")
      excluded=0
      for ex in "${COHESION_EXCLUDE[@]}"; do
        [ "$name" = "$ex" ] && excluded=1
      done
      [ "$excluded" -eq 1 ] && continue
      lines=$(wc -l < "$f" | tr -d ' ')
      exported=$(grep -cE '^type [A-Z][A-Za-z0-9]*' "$f" || true)
      if [ "$exported" -eq 1 ] && [ "$lines" -lt "$COHESION_MAX_LINES" ]; then
        small="$small $name($lines)"
      fi
    done
    # shellcheck disable=SC2086
    set -- $small
    if [ "$#" -ge "$COHESION_MIN_FILES" ]; then
      report_warn "検査 7 ファイル凝集（B-3）" \
        "$dir: 公開型 1 個・${COHESION_MAX_LINES} 行未満のファイルが $# 個ある（束ね候補）:$small"
    fi
  done
}

# --- 実行 -------------------------------------------------------------------

echo "== 規約ゲート（scripts/convention-gate.sh） =="

check_test_subject_uniqueness
check_trun_names
check_table_case_names
check_positional_case_literals
check_catalog_filenames
check_package_matches_dir
check_doc_spacing
check_file_cohesion

echo ""
if [ "$fail_count" -gt 0 ]; then
  echo "::error::規約ゲート: FAIL ${fail_count} 件 / WARN ${warn_count} 件"
  echo "規約の本文は CONVENTIONS.md と docs/testing-conventions.md にあります。"
  exit 1
fi

if [ "$warn_count" -gt 0 ]; then
  echo "規約ゲート: OK（FAIL 0 件 / WARN ${warn_count} 件 — warn はマージを止めません）"
else
  echo "規約ゲート: OK（FAIL 0 件 / WARN 0 件）"
fi
exit 0
