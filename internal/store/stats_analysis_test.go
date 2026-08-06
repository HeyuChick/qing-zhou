package store

import (
	"path/filepath"
	"testing"
	"time"
)

func newStatsStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	return st
}

// PackageStats has to answer "which package actually earns and which just sits
// there", so a package with zero sales must still appear — its absence is the
// very thing an operator is looking for.
func TestPackageStats_IncludesUnsoldAndDeleted(t *testing.T) {
	st := newStatsStore(t)
	sold, err := st.CreatePackage(Package{Name: "热销", PricePoints: 100, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreatePackage(Package{Name: "无人问津", PricePoints: 50, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	uid, err := st.CreateUser(NewUser{Username: "u1", PasswordHash: "x"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	if _, err := st.DB().Exec(`INSERT INTO orders (user_id,package_id,price_points,status,created_at)
		VALUES (?,?,?, 'success', ?), (?,?,?, 'success', ?)`,
		uid, sold, 100, now, uid, sold, 100, now); err != nil {
		t.Fatal(err)
	}
	// 一个已被删除的套餐留下的历史订单，也不该丢
	if _, err := st.DB().Exec(`INSERT INTO orders (user_id,package_id,price_points,status,created_at)
		VALUES (?, 999, 30, 'success', ?)`, uid, now); err != nil {
		t.Fatal(err)
	}

	rows, err := st.PackageStats()
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]PackageStat{}
	for _, r := range rows {
		by[r.Name] = r
	}
	if got := by["热销"]; got.Orders != 2 || got.Revenue != 200 {
		t.Errorf("热销: orders=%d revenue=%d, want 2/200", got.Orders, got.Revenue)
	}
	if _, ok := by["无人问津"]; !ok {
		t.Error("零销量的套餐没有出现——恰恰是这一行最值得看")
	}
	if _, ok := by["pkg#999"]; !ok {
		t.Error("已删除套餐的历史订单丢失了")
	}
}

// The bucket columns must count buckets, not orders: a user who buys the same
// package twice is one holder with two buckets, and conflating them would
// overstate the customer base.
func TestPackageStats_HoldersVsBuckets(t *testing.T) {
	st := newStatsStore(t)
	pkg, err := st.CreatePackage(Package{Name: "P", PricePoints: 10, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	uid, err := st.CreateUser(NewUser{Username: "u1", PasswordHash: "x"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := insertBucket(st.DB(), &Bucket{
			UserID: uid, Kind: "plan", PackageID: pkg, Name: "P",
			ClientName: "qz_u1_" + itoa(int64(i)), TrafficLimit: 1000, UsedUp: 100, UsedDown: 50,
		}); err != nil {
			t.Fatal(err)
		}
	}
	rows, _ := st.PackageStats()
	var got PackageStat
	for _, r := range rows {
		if r.PackageID == pkg {
			got = r
		}
	}
	if got.Buckets != 2 {
		t.Errorf("buckets=%d, want 2", got.Buckets)
	}
	if got.Holders != 1 {
		t.Errorf("holders=%d, want 1 —— 同一人买两次不是两个客户", got.Holders)
	}
	if got.Traffic != 300 {
		t.Errorf("traffic=%d, want 300", got.Traffic)
	}
}

// The filters are the point of the page; each one has to actually narrow.
func TestUserStats_Filters(t *testing.T) {
	st := newStatsStore(t)
	pkg, _ := st.CreatePackage(Package{Name: "P", PricePoints: 10, Enabled: true})
	now := time.Now().Unix()

	alice, _ := st.CreateUser(NewUser{Username: "alice", PasswordHash: "x", Email: "alice@e.com"})
	bob, _ := st.CreateUser(NewUser{Username: "bob", PasswordHash: "x"})
	if _, err := insertBucket(st.DB(), &Bucket{
		UserID: alice, Kind: "plan", PackageID: pkg, Name: "P", ClientName: "qz_alice",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().Exec(`UPDATE users SET status='banned', expiry_at=? WHERE id=?`, now-1, bob); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().Exec(`INSERT INTO traffic_samples (user_id,ts,up,down) VALUES (?,?,?,?)`,
		alice, now-3600, 700, 300); err != nil {
		t.Fatal(err)
	}

	names := func(f UserStatFilter) []string {
		rows, _, err := st.UserStats(f)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, r := range rows {
			out = append(out, r.Username)
		}
		return out
	}

	if got := names(UserStatFilter{}); len(got) != 2 {
		t.Errorf("无筛选 = %v, want 两个用户都在", got)
	}
	if got := names(UserStatFilter{Query: "alic"}); len(got) != 1 || got[0] != "alice" {
		t.Errorf("按用户名搜索 = %v, want [alice]", got)
	}
	if got := names(UserStatFilter{Query: "alice@e"}); len(got) != 1 {
		t.Errorf("按邮箱搜索 = %v, want [alice]", got)
	}
	if got := names(UserStatFilter{Status: "banned"}); len(got) != 1 || got[0] != "bob" {
		t.Errorf("按状态筛选 = %v, want [bob]", got)
	}
	if got := names(UserStatFilter{PackageID: pkg}); len(got) != 1 || got[0] != "alice" {
		t.Errorf("按套餐筛选 = %v, want [alice]", got)
	}
	if got := names(UserStatFilter{Expiry: "expired"}); len(got) != 1 || got[0] != "bob" {
		t.Errorf("按到期筛选 = %v, want [bob]", got)
	}

	rows, total, _ := st.UserStats(UserStatFilter{Sort: "range_traffic", Desc: true, Days: 7})
	if total != 2 {
		t.Errorf("total=%d, want 2", total)
	}
	if rows[0].Username != "alice" || rows[0].RangeTraffic != 1000 {
		t.Errorf("区间流量排序 = %s/%d, want alice/1000", rows[0].Username, rows[0].RangeTraffic)
	}
	if rows[0].Packages != "P" {
		t.Errorf("packages=%q, want P", rows[0].Packages)
	}
}

// The sort key comes straight off the query string. It must be whitelisted, not
// interpolated — an unknown key falls back instead of reaching the SQL.
func TestUserStats_RejectsUnknownSort(t *testing.T) {
	st := newStatsStore(t)
	if _, err := st.CreateUser(NewUser{Username: "u1", PasswordHash: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.UserStats(UserStatFilter{Sort: "u.id; DROP TABLE users--"}); err != nil {
		t.Fatalf("未知排序键应回退到默认值，而不是把它拼进 SQL: %v", err)
	}
	var n int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("users 表受损: n=%d err=%v", n, err)
	}
}

// Paging must be stable, or the same user shows up on two pages while another
// never appears. Ties on the sort column are broken by id.
func TestUserStats_PagingIsStable(t *testing.T) {
	st := newStatsStore(t)
	for i := 0; i < 5; i++ {
		if _, err := st.CreateUser(NewUser{Username: "u" + itoa(int64(i)), PasswordHash: "x"}); err != nil {
			t.Fatal(err)
		}
	}
	// 全部用户区间流量都是 0 —— 正是最容易出现分页错乱的情况
	seen := map[string]bool{}
	for off := 0; off < 5; off += 2 {
		rows, total, err := st.UserStats(UserStatFilter{Sort: "range_traffic", Desc: true, Limit: 2, Offset: off})
		if err != nil {
			t.Fatal(err)
		}
		if total != 5 {
			t.Fatalf("total=%d, want 5", total)
		}
		for _, r := range rows {
			if seen[r.Username] {
				t.Errorf("%s 在多页里重复出现", r.Username)
			}
			seen[r.Username] = true
		}
	}
	if len(seen) != 5 {
		t.Errorf("翻完所有页只见到 %d 个用户，want 5", len(seen))
	}
}

// The overview counters sit next to the period ones on the same row, so they
// have to count the same population. Counting the admin account in 今日新增 but
// not in 本期新增 rendered "今日 19" under "14天新增 18".
func TestOverview_ExcludesAdminConsistently(t *testing.T) {
	st := newStatsStore(t)
	if _, err := st.CreateUser(NewUser{Username: "root", PasswordHash: "x", Role: "admin"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateUser(NewUser{Username: "u1", PasswordHash: "x"}); err != nil {
		t.Fatal(err)
	}
	ov, err := st.Overview()
	if err != nil {
		t.Fatal(err)
	}
	cur, _, err := st.PeriodStats(7)
	if err != nil {
		t.Fatal(err)
	}
	if ov.TotalUsers != 1 {
		t.Errorf("total_users=%d, want 1", ov.TotalUsers)
	}
	if ov.NewToday != 1 {
		t.Errorf("new_today=%d, want 1 —— 管理员不是客户", ov.NewToday)
	}
	if ov.NewToday > cur.NewUsers {
		t.Errorf("今日新增 %d 大于本期新增 %d —— 两个数字口径不一致", ov.NewToday, cur.NewUsers)
	}
}

// PeriodStats drives the 环比 arrows, so the two windows must not overlap —
// otherwise "本期" silently includes part of "上期" and every trend reads flat.
func TestPeriodStats_WindowsDoNotOverlap(t *testing.T) {
	st := newStatsStore(t)
	uid, _ := st.CreateUser(NewUser{Username: "u1", PasswordHash: "x"})
	now := time.Now()
	// 本期内 100，上期内 40
	if _, err := st.DB().Exec(`INSERT INTO traffic_samples (user_id,ts,up,down) VALUES (?,?,?,0),(?,?,?,0)`,
		uid, now.AddDate(0, 0, -2).Unix(), 100,
		uid, now.AddDate(0, 0, -10).Unix(), 40); err != nil {
		t.Fatal(err)
	}
	cur, prev, err := st.PeriodStats(7)
	if err != nil {
		t.Fatal(err)
	}
	if cur.Traffic != 100 {
		t.Errorf("本期流量=%d, want 100", cur.Traffic)
	}
	if prev.Traffic != 40 {
		t.Errorf("上期流量=%d, want 40（两个窗口不能重叠）", prev.Traffic)
	}
}
