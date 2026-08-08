#!/usr/bin/env bash
# 規約ゲート: golangci-lint で表現できない規約を機械的に検査する。
# CI のマージ前ゲート（make conventions → make ci）で実行し、ローカルでも同じスクリプトで
# 再現できる。規約の本文は CONVENTIONS.md と docs/testing-conventions.md にあり、
# ここは「その文言のうち機械化できるもの」だけを実装する。
#
# 検査は 16 個（fail 15 個 + warn 1 個）。fail は終了コード 1、warn は報告のみで終了コード 0 のまま:
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
#   9  ドメインパッケージの単一性（B-8）                       fail
#  10  ポート宣言は ports.go にのみ置く（B-5 の (b) 全数性）    fail
#  11  ports.go はポート宣言だけを含む（B-5 の (a) 純度）       fail
#  12  内側の型はポインタで現れない（R-1 / INV-1 外向き）        fail
#  13  集約ストアポートが運ぶ型は頂点である（R-3 / INV-2）       fail
#  14  集約ルートは他の集約ルートを持たない（R-2 / INV-1 内向き） fail
#  15  集約ストアの実装は <x>_store.go にある（R-4 / INV-3）     fail
#
# 検査 10 / 11 は B-5 の `ports.go` 行が定める**双方向の約束**を、片側ずつ機械化したものである。
# 10 だけなら ports.go に何を混ぜてもよくなり、11 だけなら ports.go の外にポートを置ける。
# 対で初めて「開けばポートしか無い／ポートは全部ここにある」が成り立つ。
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

# 検査 4（カタログ的ファイル名）の禁止語幹。
# identifiers は B-3 則 1（識別子は族でまとめる）の撤回に伴って追加した。「文字列を包む識別子型」は
# 技術的な種類であって概念ではなく、value_objects と同じ species だからである。
CATALOG_STEMS=(value_objects types models utils helpers common misc identifiers)

# 検査 4 が禁じるファイル名の形。語幹ごとにこの 3 つを展開する。
#   1  <語幹>.go
#   2  <語幹>_test.go
#   3  <語幹>_<修飾>_test.go   （identifiers_property_test.go / identifiers_fuzz_test.go）
#
# 3 番目を **<語幹>*_test.go と書いてはならない**。glob の * は区切り文字を要求しないので、
# 語幹が語の途中で終わる名前を拾って誤検出する（実測: types*_test.go が typescript_test.go に、
# common*_test.go が commonly_used_test.go に一致した）。語幹の直後に区切り文字（_ または .）を
# 要求する 3 形にアンカーすることで、サフィックス付きも捕まえつつ誤検出だけを除く。
#
# 形 2 と形 3 は重ならない（identifiers_test.go は identifiers_*_test.go に一致しない）ので、
# 同じファイルが二重に報告されることはない。
CATALOG_NAME_FORMS=('%s.go' '%s_test.go' '%s_*_test.go')

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
# この除外は語幹ごと・形ごとではなく最後にまとめて効かせる（形を増やしても抜けない）。
check_catalog_filenames() {
  local stem form f
  local -a name_args
  for stem in "${CATALOG_STEMS[@]}"; do
    # find の -name 引数列を CATALOG_NAME_FORMS から組み立てる。形を 1 つ足すときに
    # 直すのは配列 1 箇所だけで、この関数は触らなくてよい。
    name_args=()
    for form in "${CATALOG_NAME_FORMS[@]}"; do
      # shellcheck disable=SC2059
      name_args+=(-o -name "$(printf "$form" "$stem")")
    done
    # 先頭の -o を落として \( ... \) に包む。
    name_args=('(' "${name_args[@]:1}" ')')

    while IFS= read -r f; do
      [ -z "$f" ] && continue
      is_generated "$f" && continue
      report_fail "検査 4 カタログ的ファイル名の不在（B-4）" \
        "$f: ファイル名は中身の概念を名指しする（禁止語幹: ${stem}）"
    done < <(find "${SCAN_DIRS[@]}" -type f "${name_args[@]}" 2>/dev/null || true)
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

# --- 検査 9: ドメインパッケージの単一性（B-8） --------------------------------
#
# 各コンテキストのドメインは internal/domain の 1 パッケージに保ち、サブパッケージへ
# 割らない。集約・値オブジェクト・ドメインイベントは 1 つのモデルとして相互に参照し合うので、
# 割れば相互 import（Go では循環 import）か、それを避けるための不自然な型の押し出しを強いられる。
#
# 検査 5（package 名 = ディレクトリ名）と対で B-8 を成す。検査 5 だけでは
# internal/domain/pricing/（package pricing）が「名前は合っている」ので素通りしてしまう。
#
# ディレクトリの存在だけでは違反にしない（.go を 1 つも持たない空ディレクトリは
# Go のパッケージではない）。testdata/ も Go ツールチェインが無視する慣習の名前なので除外する。
DOMAIN_SUBPKG_EXCLUDE='/testdata/'

check_single_domain_package() {
  local ctx domain_dir sub
  for ctx in contexts/*/; do
    domain_dir="${ctx}internal/domain"
    [ -d "$domain_dir" ] || continue
    while IFS= read -r sub; do
      [ -z "$sub" ] && continue
      report_fail "検査 9 ドメインパッケージの単一性（B-8）" \
        "$sub: ドメインは 1 コンテキスト 1 パッケージ（${domain_dir}）に保つ（型が増えたらファイルを足す）"
    done < <(find "$domain_dir" -mindepth 2 -type f -name '*.go' 2>/dev/null |
      grep -v "$DOMAIN_SUBPKG_EXCLUDE" |
      xargs -n1 dirname 2>/dev/null | sort -u || true)
  done
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

# --- 検査 10: ポート宣言は ports.go にのみ置く（B-5 の (b) 全数性） -----------
#
# 対象は contexts/*/internal/application/ の非テストファイルのうち ports.go 以外。
# shared/・アダプタ層・ドメイン層・cmd/ は対象外である（shared/outbox.Publisher の
# ような共有機構の interface は「あるコンテキストの outbound ポート」ではなく、その
# パッケージの主要型そのものだから）。
#
# go/parser を使わないのは、既存の検査 1〜9 が例外なく grep + find で書かれているからである
# （実装方針の一貫性。素朴な grep -n interface は validation.go のコメント
# `//	[domain] [application] [interfaces]` を誤検出するが、^type アンカーなら 0 件で
# 現ツリーに必要な精度は出ている）。
#
# 2 パターン見る理由: 現ツリーにグループ化 type ( ... ) 宣言は 0 件で P1 だけで足りるが、
# 「今は違反が無いから green」は検証にならない。将来グループ化で書かれた interface を
# 取りこぼさないよう P2 を併置する。
#   P1  単独の型宣言        type Foo interface {
#   P2  グループ化宣言の中身  	Foo interface {
#
# P2 の既知の偽陽性（許容）: 無名 interface のフィールド（x interface{ Foo() }）や、
# 複数行にまたがる関数引数中の無名 interface。いずれも現ツリーに存在せず、かつ
# 「application 層に無名 interface を埋め込む」こと自体が指摘に値するので fail してよい。
#
# 型引数制約の interface（type Number interface { ~int | ~float64 }）も P1 が拾って fail する。
# 除外規則は設けない（制約とポートは同じ構文で書かれるため除外の判定を機械化できず、
# 「rule はあるが何も検査していない」状態を生みやすい）。詳細は CONVENTIONS.md B-5。
check_ports_exhaustive() {
  local f n name hits
  while IFS= read -r f; do
    [ -z "$f" ] && continue
    is_generated "$f" && continue
    # 違反 0 件のとき grep は終了コード 1 を返す。set -e 下でスクリプト全体が落ちないよう
    # 既存検査と同じく || true でガードする。
    hits=$(grep -nE '^type[[:space:]]+[A-Za-z_][A-Za-z0-9_]*[[:space:]]+interface[[:space:]]*\{|^[[:space:]]+[A-Za-z_][A-Za-z0-9_]*[[:space:]]+interface[[:space:]]*\{' "$f" 2>/dev/null || true)
    [ -z "$hits" ] && continue
    while IFS= read -r hit; do
      n="${hit%%:*}"
      name=$(printf '%s' "${hit#*:}" | sed -E 's/^(type[[:space:]]+)?[[:space:]]*([A-Za-z_][A-Za-z0-9_]*)[[:space:]]+interface.*$/\2/')
      report_fail "検査 10 ポート宣言は ports.go にのみ置く（B-5）" \
        "$f:$n: application 層の interface 宣言は ports.go に集約する（見つかった宣言: ${name}）"
    done <<< "$hits"
  done < <(find contexts -type f -path '*/internal/application/*.go' ! -name '*_test.go' ! -name 'ports.go' 2>/dev/null | sort)
}

# --- 検査 11: ports.go はポート宣言だけを含む（B-5 の (a) 純度） --------------
#
# ^func に一致する行があれば fail。関数・メソッドの両方を捕まえる
# （func f(...) も func (r T) m(...) も func + 空白で始まる）。
#
# 型エイリアス（type UnitOfWork = uow.UnitOfWork[Repos]）は type で始まるので ^func に
# 原理的に一致しない。エイリアス先がインターフェースでポート語彙の一部なので、
# B-5 の (a) はこれを例外として許している。
check_ports_purity() {
  local f n hits
  while IFS= read -r f; do
    [ -z "$f" ] && continue
    is_generated "$f" && continue
    hits=$(grep -nE '^func[[:space:]]' "$f" 2>/dev/null || true)
    [ -z "$hits" ] && continue
    while IFS= read -r hit; do
      n="${hit%%:*}"
      report_fail "検査 11 ports.go はポート宣言だけを含む（B-5）" \
        "$f:$n: ports.go に func 宣言を置かない（概念を名指したファイルへ移す）"
    done <<< "$hits"
  done < <(find contexts -type f -path '*/internal/application/ports.go' 2>/dev/null | sort)
}

# =============================================================================
# 検査 12〜15: 集約境界の不変条件（INV-1 / INV-2 / INV-3）
# =============================================================================
#
# 集約とは包含の木であり、ルートとはその頂点である。この 4 本は「頂点かどうか」を
# **宣言を足さずに**コードの形から導き、境界が壊れたときに CI を止める。
#
# 走査規約（4 本に共通。ここを 1 箇所に集める）:
#   対象     12 / 14: contexts/*/internal/domain/*.go
#            13: contexts/*/internal/application/ports.go ＋ 同コンテキストの internal/domain/
#            15: contexts/*/internal/adapter/outbound/*/
#   除外     *_test.go（テストは内部を触ってよい）／生成物（手で直せない）／
#            **コメント（`//` 以降を落としてから判定する）**
#   一致     型名は前後とも [^A-Za-z0-9_] か行端で区切る。`*T` を探すときも同じ。
#            前置一致は偽陽性を出す — Reservation は ReservationRef / ReservationStatus /
#            ReservationService / ReservationLine に、Order は OrderID / OrderLine /
#            OrderStatus に、Shipment は ShipmentID / ShipmentStatus に前方一致する。
#   宣言形   単独宣言 `type X struct {` に加えグループ化宣言 `type ( … )` も見る。
#            現ツリーにドメイン型のグループ化宣言は 0 件だが、「準拠して 0」と
#            「見えずに 0」を区別できる必要がある（検査 10 が P2 を併置するのと同じ理由）。
#   決定性   ファイル列挙は sort を通す。grep -r の順序に依存しない（NFR-3）。
#
# 実装方針は既存の検査 1〜11 と同じで、go/parser を使わず grep + awk で書く。

# 語境界の左右。型名を探すときは前後ともこれで区切る。
WORD_L='(^|[^A-Za-z0-9_])'
WORD_R='([^A-Za-z0-9_]|$)'

# 走査対象のファイル一覧（非テスト・非生成物・sort 済み）を返す。
scan_go_files() {
  local dir="$1" depth="${2:-1}" f out=""
  while IFS= read -r f; do
    [ -z "$f" ] && continue
    is_generated "$f" && continue
    out="$out $f"
  done < <(find "$dir" -maxdepth "$depth" -type f -name '*.go' ! -name '*_test.go' 2>/dev/null | sort)
  printf '%s' "$out"
}

# --- 包含判定の単一実装（検査 12 と検査 13 の両方が呼ぶ） ---------------------
#
# 型 T が、同じドメインパッケージの struct のフィールドとして**含まれる**かを調べる。
# 出力は「<file>:<line>: <内容> (in <含んでいる型>)」の 0 行以上（空 = 頂点である）。
#
# **この関数が包含判定の唯一の実装である。** 検査 12（対象集合 C の算出）と
# 検査 13（頂点かどうかの判定）は同じ問いを別の入口から使うので、2 箇所に書くと
# 宣言形の網羅や語境界の扱いが片方だけ直されて、同じ型について 2 つの検査が
# 違う答えを出す状態になりうる。
#
# struct ブロックの中だけを見る（メソッド本体や複合リテラルを拾わない）。ブロックの
# 終わりは波括弧の深さで判定するので、入れ子の無名 struct フィールドがあっても崩れない。
contained_in() {
  local dir="$1" t="$2" files
  files=$(scan_go_files "$dir")
  [ -z "$files" ] && return 0
  # shellcheck disable=SC2086
  awk -v T="$t" -v WL="$WORD_L" -v WR="$WORD_R" '
    function nbraces(s,   i, c, ch) {
      c = 0
      for (i = 1; i <= length(s); i++) {
        ch = substr(s, i, 1)
        if (ch == "{") c++
        else if (ch == "}") c--
      }
      return c
    }
    function hit(s,   out) {
      out = s
      sub(/^[ \t]+/, "", out)
      printf "%s:%d: %s (in %s)\n", FILENAME, FNR, out, self
    }
    # フィールド宣言から**型の位置だけ**を取り出す。フィールド名を巻き込むと、
    # イベント構造体の `OrderID string` のような「型名と同じ名前のフィールド」で
    # その型が誤って「含まれる型」に分類され、以後 *OrderID が一律に違反扱いになる。
    #   name Type       -> Type
    #   a, b Type       -> Type
    #   Embedded        -> Embedded（埋め込みは名前が無いので丸ごと型）
    function typepart(s,   t) {
      t = s
      sub(/^[ \t]+/, "", t)
      sub(/[ \t]+$/, "", t)
      while (match(t, /^[A-Za-z_][A-Za-z0-9_]*[ \t]*,[ \t]*/)) t = substr(t, RSTART + RLENGTH)
      if (match(t, /^[A-Za-z_][A-Za-z0-9_]*[ \t]+/) && length(t) > RLENGTH) t = substr(t, RSTART + RLENGTH)
      return t
    }
    {
      line = $0
      sub(/\/\/.*/, "", line)     # コメントを落としてから判定する
      gsub(/`[^`]*`/, "", line)   # 構造体タグの中身は型ではない
      if (inblk == 0) {
        # P1 単独宣言 `type X struct {` / P2 グループ化宣言の中の `X struct {`
        if (match(line, /^(type[ \t]+)?[ \t]*[A-Za-z_][A-Za-z0-9_]*[ \t]+struct[ \t]*\{/)) {
          hdr = substr(line, RSTART, RLENGTH)
          sub(/^[ \t]*type[ \t]+/, "", hdr)
          sub(/^[ \t]+/, "", hdr)
          split(hdr, p, /[ \t]+/)
          self = p[1]
          depth = nbraces(line)
          if (depth <= 0) {
            # 1 行 struct（type X struct{ A B }）。開き括弧の後ろだけを見て閉じる。
            body = line
            sub(/^[^{]*\{/, "", body)
            sub(/\}.*$/, "", body)
            if (typepart(body) ~ (WL T WR)) hit(body)
          } else {
            inblk = 1
          }
        }
        next
      }
      d = nbraces(line)
      if (depth + d <= 0) { inblk = 0; depth = 0; next }
      depth += d
      if (typepart(line) ~ (WL T WR)) hit(line)
    }
  ' $files
}

# ドメインパッケージが宣言している型名を全て返す（P1 / P2 の両形）。
domain_type_names() {
  local dir="$1" files
  files=$(scan_go_files "$dir")
  [ -z "$files" ] && return 0
  # shellcheck disable=SC2086
  awk '
    { line = $0; sub(/\/\/.*/, "", line) }
    line ~ /^type[ \t]*\([ \t]*$/ { ingrp = 1; next }
    ingrp && line ~ /^\)/         { ingrp = 0; next }
    line ~ /^type[ \t]+[A-Za-z_][A-Za-z0-9_]*[ \t]/ {
      split(line, p, /[ \t]+/); print p[2]; next
    }
    ingrp && line ~ /^[ \t]+[A-Za-z_][A-Za-z0-9_]*[ \t]/ {
      sub(/^[ \t]+/, "", line); split(line, p, /[ \t]+/); print p[1]
    }
  ' $files | sort -u
}

# --- 検査 12: 内側の型はポインタで現れない（R-1 / INV-1 の外向き） ------------
#
# 1. 他のドメイン型に含まれる型の集合 C を得る（包含判定は contained_in が唯一の実装）
# 2. 同じドメインパッケージで T ∈ C が `*T` の形で語境界一致する箇所を探す
# 3. 1 件でもあれば fail。報告には**その型を含んでいる型の名前**を添える
#
# 担保するのは「ポインタ経由の漏洩が不可能」までであって「漏洩が不可能」ではない。
# 公開フィールド経由・複製せずに返したコレクション経由の漏洩は機械検査していない
# （CONVENTIONS.md に非担保として名指しで挙げてある）。
check_inner_types_not_pointer() {
  local ctx dom files t containment container hit
  for ctx in $(find contexts -mindepth 1 -maxdepth 1 -type d 2>/dev/null | sort); do
    dom="$ctx/internal/domain"
    [ -d "$dom" ] || continue
    files=$(scan_go_files "$dom")
    [ -z "$files" ] && continue
    while IFS= read -r t; do
      [ -z "$t" ] && continue
      containment=$(contained_in "$dom" "$t")
      [ -z "$containment" ] && continue   # 頂点なので対象外
      container=$(printf '%s' "$containment" | head -n1 | sed -E 's/^.*\(in ([A-Za-z0-9_]+)\)$/\1/')
      while IFS= read -r hit; do
        [ -z "$hit" ] && continue
        report_fail "検査 12 内側の型はポインタで現れない（R-1 / INV-1）" \
          "$hit: ${t} は ${container} に含まれる型なので、ポインタで集約の外へ出さない（値で渡す）"
      done < <(
        # shellcheck disable=SC2086
        awk -v T="$t" -v WL="$WORD_L" -v WR="$WORD_R" '
          {
            line = $0
            sub(/\/\/.*/, "", line)
            gsub(/`[^`]*`/, "", line)
            if (line ~ (WL "\\*" T WR)) {
              sub(/^[ \t]+/, "", line)
              printf "%s:%d: %s\n", FILENAME, FNR, line
            }
          }
        ' $files
      )
    done < <(domain_type_names "$dom")
  done
}

# --- 検査 13: 集約ストアポートが運ぶドメイン型は頂点である（R-3 / INV-2） -----
#
# **これが INV-2 の担保である**（検査 12 とは独立）。値型で書かれた集約ストアポート
# （Load(...) (domain.Reservation, error)）はポインタを 1 箇所も含まないため、
# `*domain.T` を軸にした導出では視界に入らない。だから軸を分けてある。
#
# 判定:
#   1. ports.go の**すべての** `type <X> interface` 宣言を集める（Repos のアクセサから
#      辿れるものだけに限らない。StockReserver / EventDispatcher / Clock のように
#      Repos に載らないポートが実在し、集約ストアを Repos の外へ置くだけで回避できてしまう）
#   2. 各ポートについて次の 2 つの位置に現れるドメイン型を集める
#      - 戻り値位置（ポインタ・値を問わない）… 読み出される集約はここに現れる
#      - 引数位置のうち**ポインタの形**のもの … 書き込み専用ストアを捕まえるために要る
#      索引キーは値の引数で現れる（Load(ctx, id domain.OrderID)）ので収集されない。
#      「キーは値・集約はポインタ」という既存コードの一貫した形がそのまま判別子になる。
#   3. 得られた型が頂点でなければ fail（含んでいる型の名前を添える）
#
# 引数と戻り値は**最初に現れる閉じ括弧**で割る。ネストした括弧を持つシグネチャ
# （関数型の引数など）が現れたら**判定せず fail させる** — 黙って誤判定しない。
#
# 残る抜け道: 「集約を**値**で受け取り、戻り値にドメイン型を持たない Save」だけは
# この 2 つの収集位置のどちらにも現れない。非担保として CONVENTIONS.md に明記してある。

# ports.go の各ポートが運ぶドメイン型を「<ポート名> <型名>」で列挙する。
# ネスト括弧のシグネチャは「<ポート名> !NESTED <行>」を出す（呼び出し側が fail させる）。
port_domain_types() {
  local f="$1"
  awk '
    function collect(s, ptronly,   rest, m, t) {
      rest = s
      while (match(rest, /\*?domain\.[A-Za-z_][A-Za-z0-9_]*/)) {
        m = substr(rest, RSTART, RLENGTH)
        rest = substr(rest, RSTART + RLENGTH)
        if (ptronly && substr(m, 1, 1) != "*") continue
        t = m; sub(/^\*/, "", t); sub(/^domain\./, "", t)
        print port, t
      }
    }
    {
      line = $0
      sub(/\/\/.*/, "", line)   # コメントを落としてから判定する
    }
    inif == 0 {
      if (match(line, /^(type[ \t]+)?[ \t]*[A-Za-z_][A-Za-z0-9_]*[ \t]+interface[ \t]*\{/)) {
        hdr = substr(line, RSTART, RLENGTH)
        sub(/^[ \t]*type[ \t]+/, "", hdr)
        sub(/^[ \t]+/, "", hdr)
        split(hdr, p, /[ \t]+/)
        port = p[1]
        inif = 1
      }
      next
    }
    line ~ /^[ \t]*\}/ { inif = 0; next }
    {
      # メソッド宣言行だけを見る（埋め込みインタフェースや空行は対象外）。
      if (line !~ /^[ \t]*[A-Za-z_][A-Za-z0-9_]*\(/) next
      op = index(line, "(")
      cl = index(line, ")")
      if (cl == 0) { print port, "!NESTED", line; next }
      args = substr(line, op + 1, cl - op - 1)
      rets = substr(line, cl + 1)
      # 最初の閉じ括弧より前にさらに開き括弧があれば、それは引数の閉じではない。
      if (index(args, "(") > 0) { print port, "!NESTED", line; next }
      collect(rets, 0)   # 戻り値位置: ポインタ・値を問わない
      collect(args, 1)   # 引数位置: ポインタの形のみ
    }
  ' "$f"
}

check_repos_are_aggregate_roots() {
  local ctx ports dom pairs port t containment container
  for ctx in $(find contexts -mindepth 1 -maxdepth 1 -type d 2>/dev/null | sort); do
    ports="$ctx/internal/application/ports.go"
    dom="$ctx/internal/domain"
    [ -f "$ports" ] || continue
    [ -d "$dom" ] || continue
    pairs=$(port_domain_types "$ports")
    [ -z "$pairs" ] && continue
    while read -r port t _; do
      [ -z "$port" ] && continue
      if [ "$t" = "!NESTED" ]; then
        report_fail "検査 13 集約ストアポートが運ぶ型は頂点である（R-3 / INV-2）" \
          "$ports: ${port}: 引数と戻り値を分割できないシグネチャ（ネストした括弧）がある。判定を諦めて fail に倒している"
        continue
      fi
      containment=$(contained_in "$dom" "$t")
      [ -z "$containment" ] && continue
      container=$(printf '%s' "$containment" | head -n1 | sed -E 's/^.*\(in ([A-Za-z0-9_]+)\)$/\1/')
      report_fail "検査 13 集約ストアポートが運ぶ型は頂点である（R-3 / INV-2）" \
        "$ports: ${port} が扱う domain.${t} は ${container} に含まれている（集約の頂点ではない）"
      printf '%s\n' "$containment" | sed 's/^/    /'
    done <<< "$pairs"
  done
}

# --- 集約ストアポートと集約ルート集合 R（検査 15 / 検査 14 が使う） ------------
#
# **定義（business-rules.md § 2.2。ここが唯一の定義箇所である）**
#   集約ストアポート = ports.go のポートのうち、**戻り値位置またはポインタ引数位置に**
#                      ドメイン型を運ぶもの
#   集約ルート集合 R = 集約ストアポートが上記 2 つの位置で運ぶドメイン型のうち、頂点であるもの
#
# 収集位置は検査 13 の判定と**同一**でなければならない。片方だけ「戻り値位置のみ」に
# すると、書き込み専用ストアの型が R に入らず検査 14 / 15 の対象から外れる。

# 集約ストアポートの名前を返す（重複排除）。
aggregate_store_ports() {
  port_domain_types "$1" | awk '$2 != "!NESTED" { print $1 }' | sort -u
}

# 集約ルート集合 R を返す（頂点であるものだけ）。
aggregate_roots() {
  local ports="$1" dom="$2" t
  while IFS= read -r t; do
    [ -z "$t" ] && continue
    [ -n "$(contained_in "$dom" "$t")" ] && continue
    printf '%s\n' "$t"
  done < <(port_domain_types "$ports" | awk '$2 != "!NESTED" { print $2 }' | sort -u)
}

# --- 検査 14: 集約ルートは他の集約ルートを持たない（R-2 / INV-1 の同一パッケージ内） ---
#
# B-8（検査 9 で機械強制済み）により 1 コンテキストにドメインパッケージは 1 つなので、
# 2 つの集約ルートは**必ず同じパッケージに同居する**。Go の可視性はパッケージ単位で
# 型単位ではないため、検査 12（ポインタで外へ出さない）だけでは同一パッケージ内の
# 集約間の漏洩を防げない。この検査がその半分を埋める。
#
# 判定: 集約ルート集合 R の各ルート A について、他のルート B が
#   (a) `type A struct { … }` のフィールド型に現れる
#   (b) `func (x *A)` / `func (x A)` で始まるメソッドのシグネチャ（引数・戻り値）に現れる
# のどちらかで語境界一致したら fail。
#
# **識別子による参照は自動的に許される。** Shipment.orderID OrderID の OrderID は
# ルート型ではないので判定の対象に入らず、加えて語境界規約により OrderID が Order に
# 前方一致することもない。この 2 段構えが要る — 意味論だけでは字面の誤検出を防げない。
#
# (a) は contained_in の出力を「含んでいる型が A のもの」で絞るだけでよい
# （包含判定を 2 度書かない）。
check_roots_dont_hold_roots() {
  local ctx ports dom roots a b hit sig
  for ctx in $(find contexts -mindepth 1 -maxdepth 1 -type d 2>/dev/null | sort); do
    ports="$ctx/internal/application/ports.go"
    dom="$ctx/internal/domain"
    [ -f "$ports" ] || continue
    [ -d "$dom" ] || continue
    roots=$(aggregate_roots "$ports" "$dom")
    [ -z "$roots" ] && continue
    for a in $roots; do
      for b in $roots; do
        [ "$a" = "$b" ] && continue
        # (a) A の struct フィールドに B が現れる
        while IFS= read -r hit; do
          [ -z "$hit" ] && continue
          report_fail "検査 14 集約ルートは他の集約ルートを持たない（R-2 / INV-1）" \
            "${hit%% (in *}: 集約ルート ${a} が別の集約ルート ${b} を保持している（集約間は識別子で参照する）"
        done < <(contained_in "$dom" "$b" | grep " (in ${a})\$" || true)
        # (b) A のメソッドのシグネチャに B が現れる
        while IFS= read -r hit; do
          [ -z "$hit" ] && continue
          report_fail "検査 14 集約ルートは他の集約ルートを持たない（R-2 / INV-1）" \
            "$hit: 集約ルート ${a} のメソッドが別の集約ルート ${b} を受け渡ししている（集約間は識別子で参照する）"
        done < <(
          # shellcheck disable=SC2086
          awk -v A="$a" -v B="$b" -v WL="$WORD_L" -v WR="$WORD_R" '
            {
              line = $0
              sub(/\/\/.*/, "", line)
              # func (x *A) … / func (x A) … で始まるメソッド宣言
              if (line !~ ("^func[ \t]*\\([ \t]*[A-Za-z_][A-Za-z0-9_]*[ \t]+\\*?" A "[ \t]*\\)")) next
              sig = line
              sub(/^func[ \t]*\([^)]*\)/, "", sig)   # レシーバを落として残りをシグネチャとする
              if (sig ~ (WL B WR)) {
                sub(/^[ \t]+/, "", line)
                printf "%s:%d: %s\n", FILENAME, FNR, line
              }
            }
          ' $(scan_go_files "$dom")
        )
      done
    done
  done
}

# --- 検査 15: 集約ストアの実装は <x>_store.go にある（R-4 / INV-3） -----------
#
# 1. 集約ストアポートの名前から `<X>` を取る（OrderStore -> Order、StockStore -> Stock）。
#    **`<X>` はポート名の語幹であり集約ルートの型名ではない** — StockStore は StockItem
#    集約のストアなので、正しい名前は stock_store.go / StockRows であって
#    stock_item_store.go / StockItemRows ではない。
# 2. 各送信アダプタパッケージで、型名を小文字化して `<x>store` で終わる型宣言を探す
# 3. その宣言を含むファイル名が `<x>_store.go` でなければ fail
#
# outboxStore が報告されないのは除外リストのおかげではない。MessagePublisher は
# 戻り値位置にもポインタ引数位置にもドメイン型を運ばないので**集約ストアポートではなく**、
# 語幹 outbox がそもそも導出されないからである。
check_aggregate_store_files() {
  local ctx ports outdir port stem stems f base decl n name lname want
  for ctx in $(find contexts -mindepth 1 -maxdepth 1 -type d 2>/dev/null | sort); do
    ports="$ctx/internal/application/ports.go"
    outdir="$ctx/internal/adapter/outbound"
    [ -f "$ports" ] || continue
    [ -d "$outdir" ] || continue

    stems=""
    while IFS= read -r port; do
      [ -z "$port" ] && continue
      case "$port" in
        *Store) stem=$(printf '%s' "${port%Store}" | tr 'A-Z' 'a-z') ;;
        *)
          # 集約ストアポートは <X>Store と名づける（R-5）。語幹を導出できない名前は
          # 検査 15 の視界から静かに外れてしまうので、ここで止める。
          report_fail "検査 15 集約ストアの実装は <x>_store.go にある（R-4 / INV-3）" \
            "$ports: 集約ストアポート ${port} が <X>Store で終わっていない（Store の語は集約ストアのポートとその実装にのみ使う）"
          continue
          ;;
      esac
      stems="$stems $stem"
    done < <(aggregate_store_ports "$ports")
    [ -z "$stems" ] && continue

    while IFS= read -r f; do
      [ -z "$f" ] && continue
      is_generated "$f" && continue
      base=$(basename "$f")
      while IFS= read -r decl; do
        [ -z "$decl" ] && continue
        n="${decl%%:*}"
        name=$(printf '%s' "${decl#*:}" | sed -E 's/^[[:space:]]*(type[[:space:]]+)?([A-Za-z_][A-Za-z0-9_]*).*$/\2/')
        lname=$(printf '%s' "$name" | tr 'A-Z' 'a-z')
        for stem in $stems; do
          case "$lname" in
            *"${stem}store")
              want="${stem}_store.go"
              if [ "$base" != "$want" ]; then
                report_fail "検査 15 集約ストアの実装は <x>_store.go にある（R-4 / INV-3）" \
                  "$f:$n: 型 ${name} は ${want} に置く（型名とファイル名から一意に辿り着けること）"
              fi
              ;;
          esac
        done
      done < <(grep -nE '^(type[[:space:]]+)?[A-Za-z_][A-Za-z0-9_]*[[:space:]]+struct[[:space:]]*\{|^type[[:space:]]+[A-Za-z_][A-Za-z0-9_]*[[:space:]]+=' "$f" 2>/dev/null || true)
    done < <(find "$outdir" -type f -name '*.go' ! -name '*_test.go' 2>/dev/null | sort)
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
check_single_domain_package
check_doc_spacing
check_file_cohesion
check_ports_exhaustive
check_ports_purity
check_inner_types_not_pointer
check_repos_are_aggregate_roots
check_roots_dont_hold_roots
check_aggregate_store_files

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
