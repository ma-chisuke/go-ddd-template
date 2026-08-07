package memory

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// countingLock は Lock の回数を数える sync.Locker。
//
// applyGroup が約束するのは「backing store あたり 1 回のロックで確定する」ことであり、
// これは外から観測できない。ロック回数を数えられる Locker を差し込むことで、その約束を
// 実行時のアサーションに変える。行ごとにロックを取り直す実装へ退行すると回数が増えるので、
// 壊れた実装がこのテストを通ることはない。
type countingLock struct {
	mu     sync.Mutex
	locked int
}

func (l *countingLock) Lock() {
	l.mu.Lock()
	l.locked++
}

func (l *countingLock) Unlock() { l.mu.Unlock() }

// staging は backing store ごとに書き込みを束ね、コミット時に store あたり 1 回だけ
// ロックを取る。マルチ SKU 予約（Save に複数の StockItem を渡す経路）の不可分性は、
// この性質そのものである。
func TestTxState_BundlesWritesPerBackingStore(t *testing.T) {
	first := &countingLock{}
	second := &countingLock{}
	var applied []string

	tx := &txState{stores: NewStores()}
	tx.stage(first, func() { applied = append(applied, "first-1") })
	tx.stage(second, func() { applied = append(applied, "second-1") })
	tx.stage(first, func() { applied = append(applied, "first-2") })
	tx.stage(first, func() { applied = append(applied, "first-3") })

	require.Len(t, tx.groups, 2, "backing store ごとに 1 グループ")
	assert.Same(t, first, tx.groups[0].lock, "グループは登録順に並ぶ")
	assert.Len(t, tx.groups[0].writes, 3, "first への書き込み")
	assert.Len(t, tx.groups[1].writes, 1, "second への書き込み")

	require.NoError(t, tx.commit(), "コミット")

	// 直撃点: 3 行の書き込みが 1 回のロックで確定している（行ごとに取り直していない）。
	assert.Equal(t, 1, first.locked, "first のロック取得回数")
	assert.Equal(t, 1, second.locked, "second のロック取得回数")
	assert.Equal(t, []string{"first-1", "first-2", "first-3", "second-1"}, applied, "適用順序")
}

// ロールバック（コミットを呼ばない）では、積んだ書き込みが 1 つも実行されず、
// ロックも取られない。
func TestTxState_DiscardsStagedWritesWithoutCommit(t *testing.T) {
	lock := &countingLock{}
	applied := 0

	tx := &txState{stores: NewStores()}
	tx.stage(lock, func() { applied++ })
	tx.stage(lock, func() { applied++ })

	assert.Equal(t, 0, applied, "コミット前に実行された書き込み")
	assert.Equal(t, 0, lock.locked, "コミット前のロック取得回数")
}
