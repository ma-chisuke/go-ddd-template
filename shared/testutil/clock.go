// Package testutil はテスト用の小さなユーティリティ(擬似時計とアサーション補助)を提供する。
//
// このパッケージは、reaper(期限切れ予約の掃除)や TTL に依存する挙動を「実時間に頼らず
// 決定的に」テストするための擬似時計 Clock を中心に据える。機構側(ドメイン／アプリケーション)は
// 実時間を直接持たず、時刻を引数や注入で受け取る設計にしておくことで、この Clock を差し込める。
//
// 本番コードからは import しない前提の補助パッケージだが、他モジュールのテストからも
// 参照できるよう、通常の(_test.go でない)Go ファイルとして提供する。
package testutil

import (
	"sync"
	"time"
)

// Clock は現在時刻を人為的に制御できる擬似時計。並行アクセスに耐えるよう mutex で守る。
// reaper のような時間依存処理を決定的にテストするために使う。
type Clock struct {
	mu  sync.Mutex
	now time.Time
}

// NewClock は指定時刻を「現在」とする擬似時計を生成する。
func NewClock(now time.Time) *Clock {
	return &Clock{now: now}
}

// Now は擬似時計の現在時刻を返す。実時間とは無関係で、Advance/Set でのみ進む。
func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance は擬似時計を d だけ進める。
func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// Set は擬似時計の現在時刻を t に設定する。
func (c *Clock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}
