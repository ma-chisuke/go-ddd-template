// 集約の種類を知らない、インメモリ backing store の共通機構。
//
// このファイルは集約ルートを 1 つ足すときに触らない。集約固有の読み取り・行変換・
// 版チェックは <集約名>_store.go 側が withLock / get を使って組み立て、ここには
// 「施錠して行を溜める」以上のことを置かない。分担は次のとおり:
//
//	rows.go            機構（集約非依存）
//	uow.go             結線
//	<集約名>_rows.go     行の型と、行 <-> 集約の変換
//	<集約名>_store.go    ポート実装（読み取り・版チェック）

package memory

import "sync"

// rows は 1 種類の行を保持するインメモリの backing store。
// 集約ごとに R を特殊化して用いる（OrderRows = rows[orderRow]）。
//
// R の中身を知らないので版も索引キーも読めない。読めないことが要点であり、
// これによって集約を足しても本型は変わらない。
type rows[R any] struct {
	mu sync.Mutex
	m  map[string]R
}

// get は 1 行を施錠して読む。
func (s *rows[R]) get(key string) (R, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	r, ok := s.m[key]
	return r, ok
}

// withLock は施錠したまま fn を実行する。版の読み取りと staging への積み込みを
// 不可分に行うために使う。fn の中で backing store の他のメソッド（get / applyGroup）を
// 呼んではならない（同じ mutex を二重に取って自分で止まる）。
func (s *rows[R]) withLock(fn func(m map[string]R) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return fn(s.m)
}

// applyGroup は writes を 1 回のロックで確定する。
//
// 「1 回」であることが要件である。同一 backing store への複数行の書き込みが不可分に
// 確定することを、全か無かの複数集約更新（在庫のマルチ SKU 予約）が前提にしている。
// 行ごとにロックを取り直す形へ崩すと、確定の途中の状態を他のゴルーチンが観測できてしまう。
func (s *rows[R]) applyGroup(writes []keyed[R]) {
	if len(writes) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, w := range writes {
		s.m[w.key] = w.row
	}
}

// keyed はキーを添えた 1 行。staging が確定順を保ったまま溜めるために使う。
type keyed[R any] struct {
	key string
	row R
}

// staging は 1 つの backing store に対する、このトランザクション中の書き込みを溜める。
// commit は backing store の applyGroup を 1 回だけ呼ぶ。
// ロールバックは staging を破棄するだけでよい（backing store は触っていない）。
type staging[R any] struct {
	target *rows[R]
	writes []keyed[R]
}

// stage は 1 行の書き込みを溜める。確定はコミット時に行う。
func (s *staging[R]) stage(key string, row R) {
	s.writes = append(s.writes, keyed[R]{key: key, row: row})
}

// commit は溜めた書き込みを backing store へ 1 回のロックで確定する。
func (s *staging[R]) commit() {
	s.target.applyGroup(s.writes)
}
