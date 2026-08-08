package memory

import outboxmem "github.com/example/go-ddd-template/shared/outbox/memory"

// Stores は配送キュー（一時的な outbox）と恒久イベントログ（events）を束ねた
// インメモリ backing store。実体は shared/outbox/memory.Stores で、UnitOfWork の
// コミット時に CommitStaged が両方へ同時に確定させる（片方だけ書く API は存在しない）。
//
// この別名により、このコンテキストの結線・テストは同一パッケージ修飾 memory.Stores /
// memory.NewStores() でアウトボックス backing store を扱える（集約の backing store である
// OrderRows / NewOrderRows と対になる）。
//
// Stores だけが Store の語を持つのは、これがアウトボックスの backing store であり
// 集約の backing store ではないためである。<X>Rows の規則の主語は「集約の」backing store で
// あり、配送キューと恒久イベントログを束ねたこの容器は主語の外にある。
type Stores = outboxmem.Stores

// NewStores は空の配送キューと空の恒久イベントログを持つ Stores を生成する。
func NewStores() *Stores {
	return outboxmem.NewStores()
}
