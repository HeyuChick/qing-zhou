package store

import (
	"testing"
	"time"
)

// The usage report's contract, in one place: traffic must be attributable to
// the package that carried it, filterable by date, and it must not survive the
// account it belonged to. Every assertion here is something an admin would read
// off the report and act on, so a silent wrong number is the failure mode.

// bucketOf returns a user's bucket id + package id for the given kind.
func bucketOf(t *testing.T, st *Store, userID int64, kind string) (bucketID, packageID int64) {
	t.Helper()
	if err := st.db.QueryRow(`SELECT id, package_id FROM user_plans WHERE user_id=? AND kind=?`,
		userID, kind).Scan(&bucketID, &packageID); err != nil {
		t.Fatalf("bucket %s for user %d: %v", kind, userID, err)
	}
	return
}

// clientNameOf returns the sing-box stats identity of a user's bucket, which is
// what AddUsageBatch is keyed by.
func clientNameOf(t *testing.T, st *Store, userID int64, kind string) string {
	t.Helper()
	var name string
	if err := st.db.QueryRow(`SELECT client_name FROM user_plans WHERE user_id=? AND kind=?`,
		userID, kind).Scan(&name); err != nil {
		t.Fatalf("client_name %s for user %d: %v", kind, userID, err)
	}
	return name
}

// backdate moves a rollup row to another day, standing in for traffic that
// arrived earlier — the poll always writes "today", so a window test cannot be
// built any other way.
func backdate(t *testing.T, st *Store, userID int64, day string) {
	t.Helper()
	if _, err := st.db.Exec(`UPDATE traffic_daily SET day=? WHERE user_id=? AND day=?`,
		day, userID, LocalDay(time.Now().Unix())); err != nil {
		t.Fatal(err)
	}
}

// A poll must land in traffic_daily attributed to the bucket's package, and
// repeated polls on the same day must accumulate into that one row rather than
// creating duplicates (the report sums them either way, but a growing row count
// per poll is the bloat the rollup exists to avoid).
func TestTrafficDaily_AttributesAndAccumulates(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "alice")
	pkg := mkPlan(t, st, "尊享套餐", 10, 100, 30)
	buy(t, st, uid, pkg)

	name := clientNameOf(t, st, uid, "plan")
	_, wantPkg := bucketOf(t, st, uid, "plan")

	for i := 0; i < 3; i++ {
		if _, err := st.AddUsageBatch(map[string]UsageDelta{name: {Up: 100, Down: 200}}); err != nil {
			t.Fatalf("poll %d: %v", i, err)
		}
	}

	var rows int
	var day string
	var gotPkg, up, down int64
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM traffic_daily WHERE user_id=?`, uid).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("traffic_daily rows = %d, want 1 (three polls on one day collapse)", rows)
	}
	if err := st.db.QueryRow(`SELECT day, package_id, up, down FROM traffic_daily WHERE user_id=?`, uid).
		Scan(&day, &gotPkg, &up, &down); err != nil {
		t.Fatal(err)
	}
	if gotPkg != wantPkg {
		t.Errorf("package_id = %d, want %d — traffic attributed to the wrong package", gotPkg, wantPkg)
	}
	if up != 300 || down != 600 {
		t.Errorf("rollup = %d/%d, want 300/600", up, down)
	}
	if day != LocalDay(time.Now().Unix()) {
		t.Errorf("day = %q, want today %q", day, LocalDay(time.Now().Unix()))
	}
}

// Traffic from two different packages held by the same user must stay separate.
// This is the question the report exists to answer, and the one the old schema
// could not.
func TestUsageByPackage_SplitsPerPackage(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "bob")
	basic := mkPlan(t, st, "基础版", 10, 100, 30)
	pro := mkPlan(t, st, "专业版", 20, 200, 30)
	buy(t, st, uid, basic)
	buy(t, st, uid, pro)

	var names []string
	rows, err := st.db.Query(`SELECT client_name FROM user_plans WHERE user_id=? AND kind='plan' ORDER BY id`, uid)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		names = append(names, n)
	}
	rows.Close()
	if len(names) != 2 {
		t.Fatalf("want 2 plan buckets, got %d", len(names))
	}

	if _, err := st.AddUsageBatch(map[string]UsageDelta{
		names[0]: {Up: 1000, Down: 2000},
		names[1]: {Up: 30, Down: 40},
	}); err != nil {
		t.Fatal(err)
	}

	got, err := st.UsageByPackageWindowed(UsageFilter{UserIDs: []int64{uid}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 package rows, got %d: %+v", len(got), got)
	}
	byName := map[string]int64{}
	for _, g := range got {
		byName[g.PackageName] = g.Up + g.Down
	}
	if byName["基础版"] != 3000 {
		t.Errorf("基础版 = %d, want 3000", byName["基础版"])
	}
	if byName["专业版"] != 70 {
		t.Errorf("专业版 = %d, want 70", byName["专业版"])
	}
}

// The date filter must actually exclude days outside the window. A report that
// ignores its own range is worse than one that has no range.
func TestUsageWindow_FiltersByDay(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "carol")
	pkg := mkPlan(t, st, "P", 10, 100, 30)
	buy(t, st, uid, pkg)
	name := clientNameOf(t, st, uid, "plan")

	// Day A (old), then day B (today).
	if _, err := st.AddUsageBatch(map[string]UsageDelta{name: {Up: 500, Down: 500}}); err != nil {
		t.Fatal(err)
	}
	old := DaysAgo(40)
	backdate(t, st, uid, old)
	if _, err := st.AddUsageBatch(map[string]UsageDelta{name: {Up: 7, Down: 3}}); err != nil {
		t.Fatal(err)
	}

	all, err := st.UsageWindowedByUser(UsageFilter{UserIDs: []int64{uid}})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].Up+all[0].Down != 1010 {
		t.Fatalf("unbounded window = %+v, want 1010 total", all)
	}

	recent, err := st.UsageWindowedByUser(UsageFilter{UserIDs: []int64{uid}, Window: UsageWindow{From: DaysAgo(29)}})
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 1 || recent[0].Up+recent[0].Down != 10 {
		t.Fatalf("last-30-days window = %+v, want only the 10 bytes from today", recent)
	}

	// A window that ends before the old day sees nothing at all.
	none, err := st.UsageWindowedByUser(UsageFilter{UserIDs: []int64{uid}, Window: UsageWindow{From: DaysAgo(60), To: DaysAgo(50)}})
	if err != nil {
		t.Fatal(err)
	}
	if len(none) != 0 {
		t.Fatalf("empty window returned %+v, want nothing", none)
	}
}

// Selecting users must select exactly those users — the report's multi-select
// is how an admin isolates one account's bill from everyone else's.
func TestUsageSelection_ScopesToChosenUsers(t *testing.T) {
	st := newRefundStore(t)
	pkg := mkPlan(t, st, "P", 10, 100, 30)
	ids := map[string]int64{}
	for _, n := range []string{"u1", "u2", "u3"} {
		id := mkUser(t, st, n)
		buy(t, st, id, pkg)
		ids[n] = id
		if _, err := st.AddUsageBatch(map[string]UsageDelta{
			clientNameOf(t, st, id, "plan"): {Up: 10, Down: 10},
		}); err != nil {
			t.Fatal(err)
		}
	}

	all, err := st.UsageWindowedByUser(UsageFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("no selection should mean everyone: got %d rows, want 3", len(all))
	}

	two, err := st.UsageWindowedByUser(UsageFilter{UserIDs: []int64{ids["u1"], ids["u3"]}})
	if err != nil {
		t.Fatal(err)
	}
	if len(two) != 2 {
		t.Fatalf("selected 2 users, got %d rows", len(two))
	}
	for _, r := range two {
		if r.UserID == ids["u2"] {
			t.Errorf("unselected user u2 leaked into the result: %+v", r)
		}
		if r.Username == "" {
			t.Errorf("row %+v has no username — the join lost it", r)
		}
	}
}

// Lifetime totals must include traffic that predates the rollup, which is the
// entire reason the report keeps two modes instead of one.
func TestUsageLifetime_IncludesPreRollupTraffic(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "dave")
	pkg := mkPlan(t, st, "老套餐", 10, 100, 30)
	buy(t, st, uid, pkg)

	// Simulate history recorded before traffic_daily existed: counters only.
	setBucketUsed(t, st, uid, "plan", 900, 100)
	if _, err := st.db.Exec(`UPDATE users SET used_up=900, used_down=100 WHERE id=?`, uid); err != nil {
		t.Fatal(err)
	}

	life, err := st.UsageLifetimeByUser(UsageFilter{UserIDs: []int64{uid}})
	if err != nil {
		t.Fatal(err)
	}
	if len(life) != 1 || life[0].Up+life[0].Down != 1000 {
		t.Fatalf("lifetime = %+v, want 1000", life)
	}

	pkgs, err := st.UsageByPackageLifetime(UsageFilter{UserIDs: []int64{uid}})
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) != 1 || pkgs[0].PackageName != "老套餐" || pkgs[0].Up+pkgs[0].Down != 1000 {
		t.Fatalf("lifetime by package = %+v, want 老套餐 with 1000", pkgs)
	}

	// The rollup, correctly, knows nothing about it.
	win, err := st.UsageWindowedByUser(UsageFilter{UserIDs: []int64{uid}})
	if err != nil {
		t.Fatal(err)
	}
	if len(win) != 0 {
		t.Fatalf("rollup should hold nothing for pre-rollup traffic, got %+v", win)
	}
}

// The synthetic package ids must be labelled, never rendered blank: an empty
// legend entry reads as a bug and an admin cannot tell pool traffic from
// unattributed history.
func TestUsagePackageLabels(t *testing.T) {
	for _, c := range []struct {
		id   int64
		name string
		want string
	}{
		{PackageIDUnattributed, "", unattributedPackageName},
		{0, "", poolPackageName},
		{7, "专业版", "专业版"},
		{7, "", "已删除套餐 #7"},
	} {
		if got := usagePackageLabel(c.id, c.name); got != c.want {
			t.Errorf("usagePackageLabel(%d, %q) = %q, want %q", c.id, c.name, got, c.want)
		}
	}
}

// Deleting a user must take their rollup rows with them. traffic_daily is never
// pruned by age, so a leftover row would inflate every site-wide total forever
// and attach a deleted account's bytes to whoever is next given that id.
func TestDeleteUser_RemovesRollup(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "erin")
	pkg := mkPlan(t, st, "P", 10, 100, 30)
	buy(t, st, uid, pkg)
	if _, err := st.AddUsageBatch(map[string]UsageDelta{
		clientNameOf(t, st, uid, "plan"): {Up: 10, Down: 10},
	}); err != nil {
		t.Fatal(err)
	}

	var before int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM traffic_daily WHERE user_id=?`, uid).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if before == 0 {
		t.Fatal("no rollup rows to begin with — test proves nothing")
	}
	if err := st.DeleteUser(uid); err != nil {
		t.Fatal(err)
	}
	var after int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM traffic_daily WHERE user_id=?`, uid).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != 0 {
		t.Errorf("traffic_daily still holds %d rows for the deleted user", after)
	}
}

// The backfill seeds days that have no rollup row, and must never double-count
// on a re-run or overwrite a day already attributed per bucket.
func TestBackfillTrafficDaily_IdempotentAndNonDestructive(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "frank")
	pkg := mkPlan(t, st, "P", 10, 100, 30)
	buy(t, st, uid, pkg)

	// A properly attributed day (today), written by the normal path.
	if _, err := st.AddUsageBatch(map[string]UsageDelta{
		clientNameOf(t, st, uid, "plan"): {Up: 5, Down: 5},
	}); err != nil {
		t.Fatal(err)
	}
	_, wantPkg := bucketOf(t, st, uid, "plan")

	// A legacy sample on an older day, with no rollup row — what an upgrading
	// install looks like.
	oldTS := time.Now().AddDate(0, 0, -3).Unix()
	if _, err := st.db.Exec(`INSERT INTO traffic_samples (user_id, ts, up, down) VALUES (?,?,?,?)`,
		uid, oldTS, 70, 30); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 3; i++ { // re-running must change nothing after the first
		if err := st.backfillTrafficDaily(); err != nil {
			t.Fatalf("backfill run %d: %v", i, err)
		}
	}

	var oldUp, oldDown, oldPkg int64
	if err := st.db.QueryRow(`SELECT up, down, package_id FROM traffic_daily WHERE user_id=? AND day=?`,
		uid, LocalDay(oldTS)).Scan(&oldUp, &oldDown, &oldPkg); err != nil {
		t.Fatalf("backfilled day missing: %v", err)
	}
	if oldUp != 70 || oldDown != 30 {
		t.Errorf("backfilled day = %d/%d, want 70/30 (re-runs must not accumulate)", oldUp, oldDown)
	}
	if oldPkg != PackageIDUnattributed {
		t.Errorf("backfilled package_id = %d, want %d (unattributable)", oldPkg, PackageIDUnattributed)
	}

	// Today's row keeps its real attribution and its exact bytes — the backfill
	// must not have folded the same traffic in a second time via its sample.
	var todayUp, todayDown, todayPkg int64
	if err := st.db.QueryRow(`SELECT up, down, package_id FROM traffic_daily WHERE user_id=? AND day=?`,
		uid, LocalDay(time.Now().Unix())).Scan(&todayUp, &todayDown, &todayPkg); err != nil {
		t.Fatal(err)
	}
	if todayUp != 5 || todayDown != 5 {
		t.Errorf("attributed day = %d/%d, want 5/5 — backfill double-counted it", todayUp, todayDown)
	}
	if todayPkg != wantPkg {
		t.Errorf("attributed day package_id = %d, want %d — backfill clobbered attribution", todayPkg, wantPkg)
	}
}

// The daily series must come back per user, in date order, so the chart can
// plot several accounts against one axis.
func TestUsageDailySeries_PerUserOrdered(t *testing.T) {
	st := newRefundStore(t)
	pkg := mkPlan(t, st, "P", 10, 100, 30)
	a := mkUser(t, st, "ua")
	b := mkUser(t, st, "ub")
	buy(t, st, a, pkg)
	buy(t, st, b, pkg)

	if _, err := st.AddUsageBatch(map[string]UsageDelta{
		clientNameOf(t, st, a, "plan"): {Up: 1, Down: 1},
		clientNameOf(t, st, b, "plan"): {Up: 2, Down: 2},
	}); err != nil {
		t.Fatal(err)
	}
	backdate(t, st, a, DaysAgo(2))
	if _, err := st.AddUsageBatch(map[string]UsageDelta{
		clientNameOf(t, st, a, "plan"): {Up: 4, Down: 4},
	}); err != nil {
		t.Fatal(err)
	}

	series, err := st.UsageDailyByUser(UsageFilter{UserIDs: []int64{a, b}})
	if err != nil {
		t.Fatal(err)
	}
	if len(series) != 2 {
		t.Fatalf("want 2 series, got %d", len(series))
	}
	for _, s := range series {
		for i := 1; i < len(s.Days); i++ {
			if s.Days[i-1].Date > s.Days[i].Date {
				t.Errorf("user %d series out of order: %s before %s", s.UserID, s.Days[i-1].Date, s.Days[i].Date)
			}
		}
	}
	var aDays int
	for _, s := range series {
		if s.UserID == a {
			aDays = len(s.Days)
		}
	}
	if aDays != 2 {
		t.Errorf("user a should have 2 distinct days, got %d", aDays)
	}

	total, err := st.UsageDailyTotal(UsageFilter{UserIDs: []int64{a, b}})
	if err != nil {
		t.Fatal(err)
	}
	var sum int64
	for _, d := range total {
		sum += d.Up + d.Down
	}
	if sum != 14 { // a: 2 + 8, b: 4
		t.Errorf("combined total = %d, want 14", sum)
	}
}

// The picker must not let a username containing LIKE wildcards match everyone.
func TestUsageUserCandidates_EscapesWildcards(t *testing.T) {
	st := newRefundStore(t)
	mkUser(t, st, "alice")
	mkUser(t, st, "100%off")

	got, err := st.UsageUserCandidates("100%", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Username != "100%off" {
		t.Errorf("search %q matched %+v, want only 100%%off", "100%", got)
	}
}

// --- 套餐维度筛选 ---

// The package filter must narrow every query the report draws from, and must
// treat the two synthetic ids (pool = 0, unattributed = -1) as selectable —
// they are exactly the entries an admin needs to isolate.
func TestUsagePackageFilter_NarrowsWindowedQueries(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "gina")
	basic := mkPlan(t, st, "基础版", 10, 100, 30)
	pro := mkPlan(t, st, "专业版", 20, 200, 30)
	buy(t, st, uid, basic)
	buy(t, st, uid, pro)

	rows, err := st.db.Query(`SELECT client_name, package_id FROM user_plans WHERE user_id=? AND kind='plan' ORDER BY id`, uid)
	if err != nil {
		t.Fatal(err)
	}
	type bk struct {
		name string
		pkg  int64
	}
	var bks []bk
	for rows.Next() {
		var b bk
		if err := rows.Scan(&b.name, &b.pkg); err != nil {
			t.Fatal(err)
		}
		bks = append(bks, b)
	}
	rows.Close()
	if len(bks) != 2 {
		t.Fatalf("want 2 plan buckets, got %d", len(bks))
	}

	if _, err := st.AddUsageBatch(map[string]UsageDelta{
		bks[0].name: {Up: 1000, Down: 1000},
		bks[1].name: {Up: 30, Down: 30},
	}); err != nil {
		t.Fatal(err)
	}

	only := UsageFilter{PackageIDs: []int64{bks[0].pkg}}

	byUser, err := st.UsageWindowedByUser(only)
	if err != nil {
		t.Fatal(err)
	}
	if len(byUser) != 1 || byUser[0].Up+byUser[0].Down != 2000 {
		t.Errorf("per-user under package filter = %+v, want 2000", byUser)
	}

	byPkg, err := st.UsageByPackageWindowed(only)
	if err != nil {
		t.Fatal(err)
	}
	if len(byPkg) != 1 || byPkg[0].PackageID != bks[0].pkg {
		t.Errorf("per-package under filter = %+v, want only package %d", byPkg, bks[0].pkg)
	}

	total, err := st.UsageDailyTotal(only)
	if err != nil {
		t.Fatal(err)
	}
	var sum int64
	for _, d := range total {
		sum += d.Up + d.Down
	}
	if sum != 2000 {
		t.Errorf("daily total under package filter = %d, want 2000", sum)
	}

	series, err := st.UsageDailyByUser(only)
	if err != nil {
		t.Fatal(err)
	}
	var ssum int64
	for _, s := range series {
		for _, d := range s.Days {
			ssum += d.Up + d.Down
		}
	}
	if ssum != 2000 {
		t.Errorf("per-user series under package filter = %d, want 2000 — the trend chart would disagree with the table", ssum)
	}
}

// Lifetime mode is the trap: users.used_* is a scalar with no package
// dimension, so under a package filter it must NOT be the source, or the KPI
// would show the account's whole lifetime beside a table showing one package.
func TestUsagePackageFilter_LifetimeUsesBucketCounters(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "hana")
	basic := mkPlan(t, st, "基础版", 10, 100, 30)
	pro := mkPlan(t, st, "专业版", 20, 200, 30)
	buy(t, st, uid, basic)
	buy(t, st, uid, pro)

	var pkgOfBasic int64
	if err := st.db.QueryRow(`SELECT package_id FROM user_plans WHERE user_id=? AND package_id=?`,
		uid, basic.ID).Scan(&pkgOfBasic); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`UPDATE user_plans SET used_up=700, used_down=300 WHERE user_id=? AND package_id=?`, uid, basic.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`UPDATE user_plans SET used_up=40, used_down=60 WHERE user_id=? AND package_id=?`, uid, pro.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`UPDATE users SET used_up=740, used_down=360 WHERE id=?`, uid); err != nil {
		t.Fatal(err)
	}

	unfiltered, err := st.UsageLifetimeByUser(UsageFilter{UserIDs: []int64{uid}})
	if err != nil {
		t.Fatal(err)
	}
	if len(unfiltered) != 1 || unfiltered[0].Up+unfiltered[0].Down != 1100 {
		t.Fatalf("unfiltered lifetime = %+v, want 1100 (the users counter)", unfiltered)
	}

	filtered, err := st.UsageLifetimeByUser(UsageFilter{UserIDs: []int64{uid}, PackageIDs: []int64{pkgOfBasic}})
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 || filtered[0].Up+filtered[0].Down != 1000 {
		t.Errorf("lifetime under package filter = %+v, want 1000 (基础版 only) — "+
			"reading users.used_* here would report the whole account", filtered)
	}

	byPkg, err := st.UsageByPackageLifetime(UsageFilter{UserIDs: []int64{uid}, PackageIDs: []int64{pkgOfBasic}})
	if err != nil {
		t.Fatal(err)
	}
	if len(byPkg) != 1 || byPkg[0].Up+byPkg[0].Down != 1000 {
		t.Errorf("lifetime by package under filter = %+v, want a single 1000 row", byPkg)
	}
	// The two must agree — they are shown side by side on one screen.
	if filtered[0].Up+filtered[0].Down != byPkg[0].Up+byPkg[0].Down {
		t.Errorf("KPI (%d) and table (%d) disagree under the same filter",
			filtered[0].Up+filtered[0].Down, byPkg[0].Up+byPkg[0].Down)
	}
}

// The pool (0) and the unattributed bucket (-1) must be selectable, and the
// picker must offer them — they have no `packages` row to join to.
func TestUsagePackageCandidates_IncludesSyntheticIDs(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "ian")
	pkg := mkPlan(t, st, "真套餐", 10, 100, 30)
	buy(t, st, uid, pkg)
	if _, err := st.AddUsageBatch(map[string]UsageDelta{
		clientNameOf(t, st, uid, "plan"): {Up: 5, Down: 5},
	}); err != nil {
		t.Fatal(err)
	}
	// Pool + pre-rollup rows, as an upgraded install would have.
	for _, r := range []struct {
		pkgID int64
		bkt   int64
		bytes int64
	}{{0, 900, 40}, {PackageIDUnattributed, 0, 70}} {
		if _, err := st.db.Exec(`INSERT INTO traffic_daily (day, user_id, bucket_id, package_id, up, down)
			VALUES (?,?,?,?,?,?)`, DaysAgo(2), uid, r.bkt, r.pkgID, r.bytes, 0); err != nil {
			t.Fatal(err)
		}
	}

	got, err := st.UsagePackageCandidates()
	if err != nil {
		t.Fatal(err)
	}
	names := map[int64]string{}
	for _, c := range got {
		names[c.ID] = c.Name
	}
	if names[0] != poolPackageName {
		t.Errorf("pool missing or unlabelled: %+v", got)
	}
	if names[PackageIDUnattributed] != unattributedPackageName {
		t.Errorf("unattributed missing or unlabelled: %+v", got)
	}
	if names[pkg.ID] != "真套餐" {
		t.Errorf("real package missing: %+v", got)
	}
	for _, c := range got {
		if c.Name == "" {
			t.Errorf("candidate %d has an empty label — renders as a blank picker row", c.ID)
		}
	}

	// And filtering to a synthetic id actually works.
	poolOnly, err := st.UsageWindowedByUser(UsageFilter{PackageIDs: []int64{0}})
	if err != nil {
		t.Fatal(err)
	}
	if len(poolOnly) != 1 || poolOnly[0].Up+poolOnly[0].Down != 40 {
		t.Errorf("pool-only filter = %+v, want 40", poolOnly)
	}
}

// User and package filters must intersect, not union.
func TestUsageFilters_Intersect(t *testing.T) {
	st := newRefundStore(t)
	pkgA := mkPlan(t, st, "A", 10, 100, 30)
	pkgB := mkPlan(t, st, "B", 10, 100, 30)
	u1 := mkUser(t, st, "j1")
	u2 := mkUser(t, st, "j2")
	for _, u := range []int64{u1, u2} {
		buy(t, st, u, pkgA)
		buy(t, st, u, pkgB)
		rows, _ := st.db.Query(`SELECT client_name FROM user_plans WHERE user_id=? AND kind='plan'`, u)
		for rows.Next() {
			var n string
			_ = rows.Scan(&n)
			if _, err := st.AddUsageBatch(map[string]UsageDelta{n: {Up: 10, Down: 0}}); err != nil {
				t.Fatal(err)
			}
		}
		rows.Close()
	}

	got, err := st.UsageWindowedByUser(UsageFilter{UserIDs: []int64{u1}, PackageIDs: []int64{pkgA.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].UserID != u1 || got[0].Up+got[0].Down != 10 {
		t.Errorf("u1 ∩ pkgA = %+v, want exactly one row of 10 bytes", got)
	}
}
