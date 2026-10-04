package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"modernc.org/sqlite"
	"qingzhou/internal/singbox"
)

// Instrument the actual SQLite driver, not an inferred query count. Counts are
// executed SQL statements; transaction BEGIN/COMMIT/ROLLBACK are excluded.
type meteringCountDriver struct {
	base  driver.Driver
	count *atomic.Int64
}

func (d meteringCountDriver) Open(name string) (driver.Conn, error) {
	c, e := d.base.Open(name)
	if e != nil {
		return nil, e
	}
	return &meteringCountConn{Conn: c, count: d.count}, nil
}

type meteringCountConn struct {
	driver.Conn
	count *atomic.Int64
}

func (c *meteringCountConn) ExecContext(ctx context.Context, q string, a []driver.NamedValue) (driver.Result, error) {
	c.count.Add(1)
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, q, a)
}
func (c *meteringCountConn) QueryContext(ctx context.Context, q string, a []driver.NamedValue) (driver.Rows, error) {
	c.count.Add(1)
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, q, a)
}
func (c *meteringCountConn) BeginTx(ctx context.Context, o driver.TxOptions) (driver.Tx, error) {
	return c.Conn.(driver.ConnBeginTx).BeginTx(ctx, o)
}
func meterSQL(t *testing.T, st *Store) *atomic.Int64 {
	t.Helper()
	if err := st.db.Close(); err != nil {
		t.Fatal(err)
	}
	count := new(atomic.Int64)
	name := fmt.Sprintf("metering-count-%d", time.Now().UnixNano())
	sql.Register(name, meteringCountDriver{base: &sqlite.Driver{}, count: count})
	db, err := sql.Open(name, st.path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)&_txlock=immediate")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(8)
	st.db = db
	return count
}

// Opt-in scale diagnostics are reproducible local/CI evidence, not a promise
// about a production VPS, WAN transport, or concurrent application workload.
func TestMeteringRelayUserScale(t *testing.T) {
	if os.Getenv("QZ_METERING_SCALE") != "1" {
		t.Skip("set QZ_METERING_SCALE=1 for 100/1000-user diagnostics")
	}
	for _, size := range []int{100, 1000} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			st := newRefundStore(t)
			st.SetSecretKey([]byte("isolated-scale-fixture"))
			a, _ := st.CreateServer(Server{Name: "entry", Host: "192.0.2.1", Enabled: true})
			b, _ := st.CreateServer(Server{Name: "landing", Host: "192.0.2.2", Enabled: true})
			landing, err := st.SaveSbInbound(&SbInbound{ServerID: b, Type: "vless", Tag: "scale-landing", ListenPort: 2443, Options: `{}`, Enabled: true})
			if err != nil {
				t.Fatal(err)
			}
			_, err = st.SaveSbInbound(&SbInbound{ServerID: a, Type: "vless", Tag: "scale-entry", ListenPort: 2443, Options: `{}`, Enabled: true, UpstreamInboundID: landing})
			if err != nil {
				t.Fatal(err)
			}
			pkg := mkPlan(t, st, "scale", 1, 100, 30)
			bindPlanToInbound(t, st, pkg.ID, "scale-entry")
			for i := 0; i < size; i++ {
				trafficCompatCustomer(t, st, pkg, "account", fmt.Sprintf("scale_user_%04d", i))
			}
			if err = st.ConfigureTrafficMetering(true, true, true); err != nil {
				t.Fatal(err)
			}
			count := meterSQL(t, st)
			count.Store(0)
			started := time.Now()
			if err = st.PrepareRelayMetering(); err != nil {
				t.Fatal(err)
			}
			prepareTime, prepareSQL := time.Since(started), count.Load()
			users, err := st.BuildUsersByTag(time.Now().Unix())
			if err != nil {
				t.Fatal(err)
			}
			down, err := st.BuildSingboxConfigForServer(b, singbox.DefaultBaseConfig, "127.0.0.1:19001", users)
			if err != nil {
				t.Fatal(err)
			}
			if err = st.RecordRelayConfigApplied(b, down); err != nil {
				t.Fatal(err)
			}
			runtime.GC()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			count.Store(0)
			started = time.Now()
			up, err := st.BuildSingboxConfigForServer(a, singbox.DefaultBaseConfig, "127.0.0.1:19002", users)
			if err != nil {
				t.Fatal(err)
			}
			buildTime, buildSQL := time.Since(started), count.Load()
			runtime.ReadMemStats(&after)
			var cfg struct {
				Outbounds []json.RawMessage `json:"outbounds"`
				Route     struct {
					Rules []json.RawMessage `json:"rules"`
				} `json:"route"`
			}
			if err = json.Unmarshal(up, &cfg); err != nil {
				t.Fatal(err)
			}
			// An unchanged prepare/build must converge to byte-identical configuration.
			if err = st.RecordRelayConfigApplied(a, up); err != nil {
				t.Fatal(err)
			}
			count.Store(0)
			started = time.Now()
			if err = st.PrepareRelayMetering(); err != nil {
				t.Fatal(err)
			}
			unchanged, err := st.BuildSingboxConfigForServer(a, singbox.DefaultBaseConfig, "127.0.0.1:19002", users)
			if err != nil {
				t.Fatal(err)
			}
			noopTime, noopSQL := time.Since(started), count.Load()
			if string(up) != string(unchanged) {
				t.Fatal("no-op compile changed config")
			}
			rows, err := st.db.Query(`SELECT identity_name FROM relay_metering_users ORDER BY id`)
			if err != nil {
				t.Fatal(err)
			}
			traffic := map[string]UsageDelta{}
			first := ""
			for rows.Next() {
				var name string
				if err = rows.Scan(&name); err != nil {
					t.Fatal(err)
				}
				traffic[name] = UsageDelta{Up: 100, Down: 1000}
				first = name
			}
			if err = rows.Close(); err != nil {
				t.Fatal(err)
			}
			if len(traffic) != size {
				t.Fatalf("per-user identities %d want %d", len(traffic), size)
			}
			poll := NewTrafficPoll(b, traffic)
			poll.Mode = "cumulative"
			poll.Epoch = "scale-epoch"
			count.Store(0)
			started = time.Now()
			if _, err = st.RecordTrafficPoll(poll); err != nil {
				t.Fatal(err)
			}
			ingestTime, ingestSQL := time.Since(started), count.Load()
			count.Store(0)
			started = time.Now()
			report, err := st.ServerServiceTraffic(b, 0)
			if err != nil {
				t.Fatal(err)
			}
			reportTime, reportSQL := time.Since(started), count.Load()
			if len(report.Users) != size || report.Total != int64(size*1100) || report.BillableTotal != 0 {
				t.Fatalf("scale accounting: users=%d total=%d charged=%d", len(report.Users), report.Total, report.BillableTotal)
			}
			plan, err := st.db.Query(`EXPLAIN QUERY PLAN SELECT r.link_id,r.user_id,l.target_server_id FROM relay_metering_users r LEFT JOIN relay_metering_links l ON l.id=r.link_id WHERE r.identity_name=?`, first)
			if err != nil {
				t.Fatal(err)
			}
			var details []string
			for plan.Next() {
				var id, parent, unused int
				var detail string
				if err = plan.Scan(&id, &parent, &unused, &detail); err != nil {
					t.Fatal(err)
				}
				details = append(details, detail)
			}
			plan.Close()
			for _, detail := range details {
				if strings.Contains(detail, "SCAN ") {
					t.Fatalf("identity lookup scans: %v", details)
				}
			}
			t.Logf("users=%d prepare=%s/%dSQL compile=%s/%dSQL alloc=%dBytes entry_config=%dBytes landing_config=%dBytes outbounds=%d rules=%d noop=%s/%dSQL ingest=%s/%dSQL report=%s/%dSQL plan=%v", size, prepareTime, prepareSQL, buildTime, buildSQL, after.TotalAlloc-before.TotalAlloc, len(up), len(down), len(cfg.Outbounds), len(cfg.Route.Rules), noopTime, noopSQL, ingestTime, ingestSQL, reportTime, reportSQL, details)
		})
	}
}
