// 在庫の SKU の往復（P-2 の一部 / R-1・R-2）と、StockItem 集約の状態機械（P-6 / R-18〜R-22）。
//
// このファイルは状態機械テストの**重い例**である（軽い例は注文側の order_property_test.go）。
// 操作は Replenish / Reserve / Confirm / Release / ReapExpired の 5 つあり、任意の操作列に
// 対する保存則・非負・冪等・期限切れの扱いを主張する。
//
// 書き方は rapid.Check + (*rapid.T).Repeat である（rapid.Run という関数は存在しない）。
// 不変条件は actions[""] に置く — ライブラリの doc が「executed before/after every other
// action invocation and should only contain invariant checking code」と定めており、
// R-18（保存則）と R-19（非負）はまさにそれにあたるからである。
//
// **この状態機械が空転しないための 2 つの仕掛け**（どちらも欠けると green のまま無検証になる）。
//
//   - 予約参照は小さな固定集合 refUniverse から引く。毎回新しい ref を作ると、同じ ref を
//     2 度使う経路が生まれず、冪等性（R-20）が一度も検証されない。
//   - reserve アクションは在庫を上回る数量も実行する。「前提を満たさなければ t.Skip」を
//     一律に当てると在庫不足の経路を毎回飛ばし、R-22 が一度も検証されない。**reserve だけは
//     例外**であり、confirm の未知 ref は飛ばしてよい（対応する性質が判定表に無いため）。
//
// 加えて R-20 / R-21 / R-22 には、前提を**構成して**必ず踏む専用の性質テストを別に置いた。
// 状態機械の探索まかせでは「たまたま一度も踏まなかった」可能性を排除できないためである。

package domain_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/example/go-ddd-template/contexts/inventory/internal/domain"
)

// 状態機械の生成域。上限は小さく取る — 主張したいのは保存則であって整数型の限界ではないし、
// 在庫が枯れやすいほど R-22（在庫不足の拒否）の経路を踏みやすい。
const (
	maxReplenishQuantity = 20
	maxReserveQuantity   = 40
)

// 時刻の生成域。
//
// StockItem.Reserve は expiresAt を **自身が呼ぶ time.Now()** から起算する（実測。設計時点の
// 「時刻は引数で受け取るのでクロック注入は不要」という読みは Reserve については誤りだった）。
// したがって ReapExpired へ渡す now は壁時計と通約可能でなければならず、固定基準
// （time.Unix(0,0)）から相対で作ると pending は決して期限切れにならず reap 経路が空転する。
//
// そこで基準時刻は 1 度だけ取り、以後は生成した相対量で駆動する。TTL と観測時刻のずれは
// 最小でも 499 時間あり、テストの実行に要する時間（ミリ秒）とは桁が隔たっているので、
// 「offset > ttl なら期限切れ」という判定は決定的である。
const (
	shortTTL  = 1 * time.Hour
	longTTL   = 1000 * time.Hour
	farFuture = 100000 * time.Hour
)

// ttlChoices は Reserve に渡す TTL の候補。
var ttlChoices = []time.Duration{shortTTL, longTTL}

// reapOffsets は ReapExpired に渡す now の、基準時刻からのずれ。
// 過去（何も期限切れにならない）・中間（shortTTL だけ期限切れ）・遠い未来（すべて期限切れ）の 3 点。
var reapOffsets = []time.Duration{-1000 * time.Hour, 500 * time.Hour, farFuture}

// refUniverse は状態機械が使う予約参照の**小さな固定集合**。
// 集合を小さく取ることで既出の ref が高い確率で選び直され、冪等経路を必ず踏む。
var refUniverse = []string{"REF-1", "REF-2", "REF-3"}

// newIdentifierGen は識別子の**成功側**の文字列生成器を返す。
//
// 非空かつ前後に空白を含まない形に絞る。New<T> は入力を TrimSpace してから包むので、
// 前後に空白のある文字列は往復しない（New(s).String() != s）。これは実装のバグではなく
// 「前後の空白は意味を持たない」という仕様なので、往復を主張する生成域から外す。
func newIdentifierGen() *rapid.Generator[string] {
	return rapid.StringMatching(`[0-9A-Za-z][0-9A-Za-z ._-]{0,14}[0-9A-Za-z]|[0-9A-Za-z]`)
}

// newBlankGen は識別子の**失敗側**の文字列生成器を返す（空文字と空白のみの文字列）。
func newBlankGen() *rapid.Generator[string] {
	return rapid.StringOfN(rapid.SampledFrom([]rune{' ', '\t', '\n', '\r'}), 0, 6, -1)
}

// mustSKUOf は文字列から SKU を組み立てる（生成器が不正な値を作ったら中断する）。
func mustSKUOf(t *rapid.T, s string) domain.SKU {
	t.Helper()
	sku, err := domain.NewSKU(s)
	require.NoError(t, err, "生成器が不正な SKU を作った")
	return sku
}

// mustRefOf は文字列から ReservationRef を組み立てる。
func mustRefOf(t *rapid.T, s string) domain.ReservationRef {
	t.Helper()
	ref, err := domain.NewReservationRef(s)
	require.NoError(t, err, "生成器が不正な ReservationRef を作った")
	return ref
}

// mustQuantityOf は int から Quantity を組み立てる。
func mustQuantityOf(t *rapid.T, n int) domain.Quantity {
	t.Helper()
	qty, err := domain.NewQuantity(n)
	require.NoError(t, err, "生成器が不正な Quantity を作った")
	return qty
}

// newSeededItem は指定数量まで補充した在庫項目を組み立てる（0 なら補充しない）。
func newSeededItem(t *rapid.T, available int) *domain.StockItem {
	t.Helper()
	item, err := domain.NewStockItem("stock-1", mustSKUOf(t, "WIDGET-001"))
	require.NoError(t, err, "在庫項目の生成に失敗した")
	if available > 0 {
		require.NoError(t, item.Replenish(mustQuantityOf(t, available)), "補充に失敗した")
	}
	return item
}

// stockSnapshot は集約の観測できる状態（利用可能・予約済み）の控え。
type stockSnapshot struct {
	available int
	reserved  int
}

// newSnapshot は集約の現在の観測できる状態を控える。
func newSnapshot(item *domain.StockItem) stockSnapshot {
	return stockSnapshot{available: item.Available().Int(), reserved: item.Reserved().Int()}
}

// newReservationsByStatus は集約が保持する予約のうち指定状態のものを ref -> 数量の表にする。
func newReservationsByStatus(item *domain.StockItem, status domain.ReservationStatus) map[string]int {
	out := make(map[string]int)
	for _, r := range item.Reservations() {
		if r.Status() == status {
			out[r.Ref().String()] = r.Quantity().Int()
		}
	}
	return out
}

// TestSKU_RoundTripsAndRejectsBlank は在庫コンテキストの SKU について R-1（正）と
// R-2（負）を主張する。
//
// 注文コンテキストの SKU は**別パッケージの別の型**であり、性質テストを共有しない。
func TestSKU_RoundTripsAndRejectsBlank(t *testing.T) {
	t.Parallel()

	t.Run("性質: 空白を前後に持たない非空文字列は New と String で往復する", func(t *testing.T) {
		t.Parallel()

		rapid.Check(t, func(t *rapid.T) {
			s := newIdentifierGen().Draw(t, "s")
			sku, err := domain.NewSKU(s)
			require.NoError(t, err, "非空文字列からは生成できる")
			assert.Equal(t, s, sku.String(), "String は入力をそのまま返す")
		})
	})

	t.Run("性質: 空白のみの文字列は ErrInvalidSKU とゼロ値を返す", func(t *testing.T) {
		t.Parallel()

		rapid.Check(t, func(t *rapid.T) {
			s := newBlankGen().Draw(t, "s")
			sku, err := domain.NewSKU(s)
			require.ErrorIs(t, err, domain.ErrInvalidSKU, "空白のみは番兵で拒否される")
			assert.Equal(t, domain.SKU{}, sku, "失敗時の返り値はゼロ値")
		})
	})
}

// stockModel は状態機械が集約と突き合わせるための参照実装（モデル）。
//
// 集約の実装を写したものではなく、**仕様をそのまま素朴に書いた**ものである点が要点である。
// 実装と同じ手順でモデルを更新すると、両方が同時に壊れたときに一致してしまう。
type stockModel struct {
	// total は available + reserved の総和。Replenish 以外のどの操作でも変えない（R-18）。
	total int
	// available は利用可能在庫。
	available int
	// pending / confirmed は ref -> 予約数量。
	pending   map[string]int
	confirmed map[string]int
	// ttl は pending の予約に与えた TTL。ReapExpired の結果を予測するために持つ。
	ttl map[string]time.Duration
}

// newStockModel は空の在庫項目に対応するモデルを作る。
func newStockModel() *stockModel {
	return &stockModel{
		pending:   make(map[string]int),
		confirmed: make(map[string]int),
		ttl:       make(map[string]time.Duration),
	}
}

// holds は ref に有効な予約（pending / confirmed のいずれか）があるかを返す。
func (m *stockModel) holds(ref string) bool {
	_, pending := m.pending[ref]
	_, confirmed := m.confirmed[ref]
	return pending || confirmed
}

// reservedTotal は有効な予約の数量合計を返す。
func (m *stockModel) reservedTotal() int {
	sum := 0
	for _, qty := range m.pending {
		sum += qty
	}
	for _, qty := range m.confirmed {
		sum += qty
	}
	return sum
}

// assertMatchesModel は集約の観測できる状態がモデルと一致することを主張する。
func assertMatchesModel(t *rapid.T, item *domain.StockItem, m *stockModel) {
	t.Helper()
	assert.Equal(t, m.available, item.Available().Int(), "利用可能在庫がモデルと一致する")
	assert.Equal(t, m.reservedTotal(), item.Reserved().Int(), "予約済み数量がモデルと一致する")
	assert.Equal(t, m.pending, newReservationsByStatus(item, domain.ReservationPending), "pending 予約がモデルと一致する")
	assert.Equal(t, m.confirmed, newReservationsByStatus(item, domain.ReservationConfirmed), "confirmed 予約がモデルと一致する")
}

// TestStockItem_StateMachine は StockItem 集約の状態機械であり、R-18（保存則）・R-19（非負）・
// R-20（冪等）・R-21（confirmed は期限切れしない）・R-22（在庫不足の拒否）を主張する。
func TestStockItem_StateMachine(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		item, err := domain.NewStockItem("stock-1", mustSKUOf(t, "WIDGET-001"))
		require.NoError(t, err, "在庫項目の生成に失敗した")
		model := newStockModel()
		// 基準時刻はここで 1 度だけ取る（理由は上の時刻の生成域の説明を参照）。
		wallBase := time.Now().UTC()

		t.Repeat(map[string]func(*rapid.T){
			// 不変条件のみ。各アクションの前後で毎回実行される（skip されたアクションの前後も含む）。
			"": func(t *rapid.T) {
				// R-18 保存則: モデルの total は replenish アクションでしか増えないので、
				// ここで一致を主張することがそのまま「他の操作は総和を変えない」の主張になる。
				assert.Equal(t, model.total, item.Available().Int()+item.Reserved().Int(),
					"available + reserved の総和は Replenish 以外では変化しない")
				// R-19 非負。
				assert.GreaterOrEqual(t, item.Available().Int(), 0, "Available() は常に非負")
			},

			"replenish": func(t *rapid.T) {
				qty := rapid.IntRange(0, maxReplenishQuantity).Draw(t, "replenishQuantity")
				before := newSnapshot(item)

				err := item.Replenish(mustQuantityOf(t, qty))
				if qty == 0 {
					// 補充数量 0 は集約が弾く。拒否された補充は総和を増やさない（R-18 の裏）。
					require.ErrorIs(t, err, domain.ErrInvalidQuantity, "補充数量 0 は拒否される")
					assert.Equal(t, before, newSnapshot(item), "拒否された補充は状態を変えない")
					return
				}
				require.NoError(t, err, "1 以上の補充は成功する")
				// R-18: Replenish のみが総和を増やし、増分は与えた数量に一致する。
				model.total += qty
				model.available += qty
				assertMatchesModel(t, item, model)
			},

			"reserve": func(t *rapid.T) {
				ref := rapid.SampledFrom(refUniverse).Draw(t, "reserveRef")
				// **在庫を上回る数量も引く。** ここで前提を満たす数量だけに絞ると R-22 が空転する。
				qty := rapid.IntRange(0, maxReserveQuantity).Draw(t, "reserveQuantity")
				ttl := rapid.SampledFrom(ttlChoices).Draw(t, "reserveTTL")
				before := newSnapshot(item)

				err := item.Reserve(mustRefOf(t, ref), mustQuantityOf(t, qty), ttl)
				switch {
				case qty == 0:
					// 集約は数量 0 を最初に弾く（既存予約の有無より先に評価される）。
					require.ErrorIs(t, err, domain.ErrInvalidQuantity, "予約数量 0 は拒否される")
					assert.Equal(t, before, newSnapshot(item), "拒否された予約は状態を変えない")
				case model.holds(ref):
					// R-20 冪等: 有効な予約がある ref への再予約は、在庫数と無関係に no-op で成功する
					//（実装は既存予約の判定を在庫の判定より先に置いている）。
					require.NoError(t, err, "有効な予約がある ref への再予約は冪等 no-op")
					assert.Equal(t, before, newSnapshot(item), "冪等 no-op は状態を変えない")
				case qty > model.available:
					// R-22: 在庫を上回る予約は拒否され、状態は変化しない。
					require.ErrorIs(t, err, domain.ErrInsufficientStock, "在庫を上回る予約は拒否される")
					assert.Equal(t, before, newSnapshot(item), "拒否は副作用を残さない")
				default:
					require.NoError(t, err, "在庫以下の新規予約は成功する")
					model.available -= qty
					model.pending[ref] = qty
					model.ttl[ref] = ttl
					assertMatchesModel(t, item, model)
				}
			},

			"confirm": func(t *rapid.T) {
				ref := rapid.SampledFrom(refUniverse).Draw(t, "confirmRef")
				if !model.holds(ref) {
					// 未知の ref への Confirm（ErrReservationNotFound）は判定表に対応する性質が
					// 無いので飛ばす。**reserve は同じ理由で飛ばしてはいけない** — R-22 が空転する。
					t.Skip("有効な予約が無い ref")
				}
				before := newSnapshot(item)

				require.NoError(t, item.Confirm(mustRefOf(t, ref)), "有効な予約の確定は成功する")
				// R-20 冪等でもあり、confirmed 化が在庫数を動かさないことでもある。
				assert.Equal(t, before, newSnapshot(item), "確定は available と reserved を変えない")
				if qty, pending := model.pending[ref]; pending {
					delete(model.pending, ref)
					delete(model.ttl, ref)
					model.confirmed[ref] = qty
				}
				assertMatchesModel(t, item, model)
			},

			"release": func(t *rapid.T) {
				ref := rapid.SampledFrom(refUniverse).Draw(t, "releaseRef")
				before := newSnapshot(item)

				// 未知・解放済みの ref でも成功する（冪等 no-op）ので skip しない。
				require.NoError(t, item.Release(mustRefOf(t, ref)), "解放は未知の ref でも成功する")
				if !model.holds(ref) {
					// R-20 冪等: 解放済み・未知の ref への再解放は状態を変えない。
					assert.Equal(t, before, newSnapshot(item), "再解放は状態を変えない")
					return
				}
				qty := model.pending[ref] + model.confirmed[ref]
				model.available += qty
				delete(model.pending, ref)
				delete(model.confirmed, ref)
				delete(model.ttl, ref)
				assertMatchesModel(t, item, model)
			},

			"reap": func(t *rapid.T) {
				offset := rapid.SampledFrom(reapOffsets).Draw(t, "reapOffset")

				// モデル側で「解放されるはず」の pending を先に決める。
				var expired []string
				for ref, ttl := range model.ttl {
					if offset > ttl {
						expired = append(expired, ref)
					}
				}

				events := item.ReapExpired(wallBase.Add(offset))
				assert.Len(t, events, len(expired), "期限切れの pending の数だけ解放イベントが出る")
				for _, ref := range expired {
					model.available += model.pending[ref]
					delete(model.pending, ref)
					delete(model.ttl, ref)
				}
				// R-21: confirmed 予約は now をどれだけ進めても解放されない。
				// モデルの confirmed は reap で一切変えていないので、この突き合わせが
				// そのまま「confirmed は解放されなかった」の主張になる。
				assertMatchesModel(t, item, model)
			},
		})
	})
}

// TestStockItem_OperationsAreIdempotent は R-20 を、同一 ref の**再実行を必ず起こして**主張する。
//
// 状態機械も既出の ref を選び直すが、探索まかせでは「一度も再実行しなかった」可能性を
// 排除できない。ここでは Reserve / Confirm / Release のそれぞれを 2 回以上繰り返し、
// 初回実行後の状態と一致することを毎回観測する。
func TestStockItem_OperationsAreIdempotent(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		stock := rapid.IntRange(1, maxReplenishQuantity).Draw(t, "stock")
		qty := rapid.IntRange(1, stock).Draw(t, "quantity")
		repeats := rapid.IntRange(2, 5).Draw(t, "repeats")

		item := newSeededItem(t, stock)
		ref := mustRefOf(t, "REF-1")

		require.NoError(t, item.Reserve(ref, mustQuantityOf(t, qty), shortTTL), "初回の予約は成功する")
		afterReserve := newSnapshot(item)
		for range repeats - 1 {
			require.NoError(t, item.Reserve(ref, mustQuantityOf(t, qty), shortTTL), "同一 ref への再予約は冪等 no-op")
		}
		assert.Equal(t, afterReserve, newSnapshot(item), "再予約は状態を変えない")

		require.NoError(t, item.Confirm(ref), "初回の確定は成功する")
		afterConfirm := newSnapshot(item)
		for range repeats - 1 {
			require.NoError(t, item.Confirm(ref), "同一 ref への再確定は冪等 no-op")
		}
		assert.Equal(t, afterConfirm, newSnapshot(item), "再確定は状態を変えない")

		require.NoError(t, item.Release(ref), "初回の解放は成功する")
		afterRelease := newSnapshot(item)
		for range repeats - 1 {
			require.NoError(t, item.Release(ref), "同一 ref への再解放は冪等 no-op")
		}
		assert.Equal(t, afterRelease, newSnapshot(item), "再解放は状態を変えない")
		assert.Equal(t, stock, item.Available().Int(), "解放後は補充した在庫がすべて戻る")
	})
}

// TestStockItem_ReserveBeyondAvailableIsRejected は R-22 を、前提を**構成して**主張する。
//
// 状態機械も在庫を上回る数量を引くが、探索まかせでは「一度も上回らなかった」可能性を
// 排除できない。ここでは available + 1 以上の数量を必ず作り、拒否と状態不変を毎回観測する。
func TestStockItem_ReserveBeyondAvailableIsRejected(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		stock := rapid.IntRange(0, maxReplenishQuantity).Draw(t, "stock")
		excess := rapid.IntRange(1, maxReplenishQuantity).Draw(t, "excess")

		item := newSeededItem(t, stock)
		before := newSnapshot(item)

		err := item.Reserve(mustRefOf(t, "REF-1"), mustQuantityOf(t, stock+excess), shortTTL)
		require.ErrorIs(t, err, domain.ErrInsufficientStock, "在庫を上回る予約は拒否される")
		assert.Equal(t, before, newSnapshot(item), "拒否は副作用を残さない")
		assert.Empty(t, item.Reservations(), "拒否された予約は記録されない")
	})
}

// TestStockItem_ReapNeverReleasesConfirmed は R-21 を、前提（confirmed 予約が存在し、now が
// TTL を大きく超える）を**構成して**主張する。
//
// アサーションは決定的な対である。pending が消えたこと（負）と confirmed が残ったこと（正）を
// 1 回の観測で同時に満たす。前者だけなら「すべて解放する」実装でも、後者だけなら
// 「何も解放しない」実装でも満たせてしまう。
func TestStockItem_ReapNeverReleasesConfirmed(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		stock := rapid.IntRange(2, maxReplenishQuantity).Draw(t, "stock")
		confirmedQty := rapid.IntRange(1, stock-1).Draw(t, "confirmedQuantity")
		pendingQty := rapid.IntRange(1, stock-confirmedQty).Draw(t, "pendingQuantity")
		ttl := rapid.SampledFrom(ttlChoices).Draw(t, "ttl")

		item := newSeededItem(t, stock)
		confirmedRef := mustRefOf(t, "REF-CONFIRMED")
		pendingRef := mustRefOf(t, "REF-PENDING")
		require.NoError(t, item.Reserve(confirmedRef, mustQuantityOf(t, confirmedQty), ttl), "確定用の予約")
		require.NoError(t, item.Confirm(confirmedRef), "確定")
		require.NoError(t, item.Reserve(pendingRef, mustQuantityOf(t, pendingQty), ttl), "期限切れにする予約")

		// now を TTL よりはるか先へ進める。longTTL でも確実に超える幅を取っている。
		events := item.ReapExpired(time.Now().UTC().Add(farFuture))

		assert.Len(t, events, 1, "期限切れの pending 1 件だけが解放される")
		assert.Empty(t, newReservationsByStatus(item, domain.ReservationPending), "pending は解放されて残らない")
		assert.Equal(t, map[string]int{confirmedRef.String(): confirmedQty},
			newReservationsByStatus(item, domain.ReservationConfirmed), "confirmed は解放されない")
		assert.Equal(t, stock-confirmedQty, item.Available().Int(), "解放されたのは pending の数量だけ")
	})
}
