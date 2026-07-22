package store

import (
	"log"
	"strings"
)

// schema is applied idempotently on every boot. Tables for later phases
// (packages, orders, nodes, groups, ...) are added in their own phases.
const schema = `
CREATE TABLE IF NOT EXISTS settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS users (
  id              INTEGER PRIMARY KEY AUTOINCREMENT,
  username        TEXT    NOT NULL UNIQUE,
  email           TEXT    UNIQUE,
  password_hash   TEXT    NOT NULL,
  role            TEXT    NOT NULL DEFAULT 'user',
  status          TEXT    NOT NULL DEFAULT 'active',
  email_verified  INTEGER NOT NULL DEFAULT 0,
  points          INTEGER NOT NULL DEFAULT 0,
  client_id     INTEGER,
  client_name   TEXT,
  client_uuid   TEXT,
  client_secret TEXT,
  sub_token       TEXT    UNIQUE,
  current_plan_id INTEGER,
  traffic_limit   INTEGER NOT NULL DEFAULT 0,
  device_limit    INTEGER NOT NULL DEFAULT 3,
  used_up         INTEGER NOT NULL DEFAULT 0,
  used_down       INTEGER NOT NULL DEFAULT 0,
  expiry_at       INTEGER NOT NULL DEFAULT 0,
  created_at      INTEGER NOT NULL,
  updated_at      INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_users_email ON users(email);

CREATE TABLE IF NOT EXISTS packages (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  type          TEXT    NOT NULL,            -- traffic | plan | device
  name          TEXT    NOT NULL,
  description   TEXT    NOT NULL DEFAULT '',
  highlights    TEXT    NOT NULL DEFAULT '',   -- JSON array of selling-point bullets
  price_points  INTEGER NOT NULL DEFAULT 0,
  traffic_bytes INTEGER NOT NULL DEFAULT 0,
  device_add    INTEGER NOT NULL DEFAULT 0,
  duration_days INTEGER NOT NULL DEFAULT 0,
  stock         INTEGER NOT NULL DEFAULT -1, -- -1 = unlimited
  enabled       INTEGER NOT NULL DEFAULT 1,
  sort_order    INTEGER NOT NULL DEFAULT 0,
  created_at    INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS orders (
  id               INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id          INTEGER NOT NULL,
  package_id       INTEGER NOT NULL,
  package_snapshot TEXT    NOT NULL DEFAULT '',
  price_points     INTEGER NOT NULL,
  status           TEXT    NOT NULL,
  created_at       INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_orders_user ON orders(user_id);

CREATE TABLE IF NOT EXISTS point_transactions (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id       INTEGER NOT NULL,
  amount        INTEGER NOT NULL,            -- + credit, - debit
  type          TEXT    NOT NULL,            -- admin_recharge | purchase | signup_bonus | refund | adjust
  balance_after INTEGER NOT NULL,
  ref_id        INTEGER NOT NULL DEFAULT 0,  -- order id when type=purchase
  note          TEXT    NOT NULL DEFAULT '',
  operator_id   INTEGER NOT NULL DEFAULT 0,
  created_at    INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_ptx_user ON point_transactions(user_id);

CREATE TABLE IF NOT EXISTS device_addons (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id      INTEGER NOT NULL,
  slots        INTEGER NOT NULL,
  order_id     INTEGER NOT NULL DEFAULT 0,
  purchased_at INTEGER NOT NULL,
  expires_at   INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_device_addons_user ON device_addons(user_id, expires_at);

CREATE TABLE IF NOT EXISTS email_tokens (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id    INTEGER NOT NULL,
  token      TEXT    NOT NULL UNIQUE,
  purpose    TEXT    NOT NULL,            -- verify | reset
  expires_at INTEGER NOT NULL,
  used       INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_email_tokens_token ON email_tokens(token);

CREATE TABLE IF NOT EXISTS nodes (
  id              INTEGER PRIMARY KEY AUTOINCREMENT,
  type            TEXT    NOT NULL,          -- self_built | external
  name            TEXT    NOT NULL,
  protocol        TEXT    NOT NULL DEFAULT '',
  inbound_tag TEXT    NOT NULL DEFAULT '', -- self_built: matches sing-box inbound tag
  share_link      TEXT    NOT NULL DEFAULT '', -- external: raw share URI
  source_id       INTEGER NOT NULL DEFAULT 0,
  enabled         INTEGER NOT NULL DEFAULT 1,
  sort_order      INTEGER NOT NULL DEFAULT 0,
  created_at      INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_nodes_enabled ON nodes(enabled);

CREATE TABLE IF NOT EXISTS node_groups (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  name        TEXT    NOT NULL,
  description TEXT    NOT NULL DEFAULT '',
  sort_order  INTEGER NOT NULL DEFAULT 0,
  created_at  INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS node_group_members (
  node_id  INTEGER NOT NULL,
  group_id INTEGER NOT NULL,
  PRIMARY KEY (node_id, group_id)
);
CREATE INDEX IF NOT EXISTS idx_ngm_group ON node_group_members(group_id);
CREATE INDEX IF NOT EXISTS idx_ngm_node  ON node_group_members(node_id);

CREATE TABLE IF NOT EXISTS plan_groups (
  package_id INTEGER NOT NULL,
  group_id   INTEGER NOT NULL,
  PRIMARY KEY (package_id, group_id)
);
CREATE INDEX IF NOT EXISTS idx_plan_groups_pkg ON plan_groups(package_id);

-- User groups gate WHO MAY BUY a package. Do not confuse with node_groups,
-- which gate WHICH NODES a bought package grants (users → packages here vs
-- packages → nodes there); the two are independent axes.
CREATE TABLE IF NOT EXISTS user_groups (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  name        TEXT    NOT NULL,
  description TEXT    NOT NULL DEFAULT '',
  sort_order  INTEGER NOT NULL DEFAULT 0,
  created_at  INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS user_group_members (
  user_id  INTEGER NOT NULL,
  group_id INTEGER NOT NULL,
  PRIMARY KEY (user_id, group_id)
);
CREATE INDEX IF NOT EXISTS idx_ugm_group ON user_group_members(group_id);
CREATE INDEX IF NOT EXISTS idx_ugm_user  ON user_group_members(user_id);

-- package_user_groups restricts a package to the listed user groups. NO ROWS
-- for a package means "public": anyone may buy it. That keeps every package
-- that existed before this feature buyable after the upgrade.
CREATE TABLE IF NOT EXISTS package_user_groups (
  package_id INTEGER NOT NULL,
  group_id   INTEGER NOT NULL,
  PRIMARY KEY (package_id, group_id)
);
CREATE INDEX IF NOT EXISTS idx_pug_pkg   ON package_user_groups(package_id);
CREATE INDEX IF NOT EXISTS idx_pug_group ON package_user_groups(group_id);

-- Registration codes may auto-join their redeemer into user groups.
CREATE TABLE IF NOT EXISTS reg_code_user_groups (
  code_id  INTEGER NOT NULL,
  group_id INTEGER NOT NULL,
  PRIMARY KEY (code_id, group_id)
);
CREATE INDEX IF NOT EXISTS idx_rcug_code ON reg_code_user_groups(code_id);

CREATE TABLE IF NOT EXISTS reg_codes (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  code       TEXT    NOT NULL UNIQUE,
  max_uses   INTEGER NOT NULL DEFAULT 1,  -- 0 = unlimited
  used       INTEGER NOT NULL DEFAULT 0,
  enabled    INTEGER NOT NULL DEFAULT 1,
  note       TEXT    NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL
);

-- who consumed a reg code (username/email snapshotted so it survives user delete)
CREATE TABLE IF NOT EXISTS reg_code_uses (
  id       INTEGER PRIMARY KEY AUTOINCREMENT,
  code_id  INTEGER NOT NULL,
  user_id  INTEGER NOT NULL DEFAULT 0,
  username TEXT    NOT NULL DEFAULT '',
  email    TEXT    NOT NULL DEFAULT '',
  used_at  INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_rcu_code ON reg_code_uses(code_id);

-- per-user node blocklist: a node_key present here is hidden from that user's
-- subscription output (only affects the owning user). node_key = subconv.NodeKey.
CREATE TABLE IF NOT EXISTS user_disabled_nodes (
  user_id  INTEGER NOT NULL,
  node_key TEXT    NOT NULL,
  PRIMARY KEY (user_id, node_key)
);

CREATE TABLE IF NOT EXISTS sessions (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id    INTEGER NOT NULL,
  jti        TEXT    NOT NULL UNIQUE,
  ip         TEXT    NOT NULL DEFAULT '',
  user_agent TEXT    NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  last_seen  INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_sessions_user ON sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_sessions_jti  ON sessions(jti);

CREATE TABLE IF NOT EXISTS announcements (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  title      TEXT    NOT NULL,
  content    TEXT    NOT NULL DEFAULT '',
  pinned     INTEGER NOT NULL DEFAULT 0,
  enabled    INTEGER NOT NULL DEFAULT 1,
  start_at   INTEGER NOT NULL DEFAULT 0,
  end_at     INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS announcement_reads (
  user_id         INTEGER NOT NULL,
  announcement_id INTEGER NOT NULL,
  read_at         INTEGER NOT NULL,
  PRIMARY KEY (user_id, announcement_id)
);

CREATE TABLE IF NOT EXISTS node_sources (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  name         TEXT    NOT NULL,
  url          TEXT    NOT NULL,
  type         TEXT    NOT NULL DEFAULT 'base64', -- base64 | clash
  enabled      INTEGER NOT NULL DEFAULT 1,
  last_fetched INTEGER NOT NULL DEFAULT 0,
  last_count   INTEGER NOT NULL DEFAULT 0,
  last_error   TEXT    NOT NULL DEFAULT '',
  group_ids    TEXT    NOT NULL DEFAULT '',   -- JSON array of group ids applied to imported nodes
  created_at   INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS help_docs (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  title      TEXT    NOT NULL,
  content    TEXT    NOT NULL DEFAULT '',   -- markdown
  sort_order INTEGER NOT NULL DEFAULT 0,
  published  INTEGER NOT NULL DEFAULT 1,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

-- ===== Native sing-box management (B2: 轻舟 replaces sing-box) =====
-- TLS / Reality profiles attached to inbounds. server_json holds the sing-box
-- inbound "tls" block (with the Reality private_key) and is stored ENCRYPTED.
-- client_json holds the client-side params (sni/pbk/sid/alpn/fp) used to build
-- share links.
CREATE TABLE IF NOT EXISTS sb_tls (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  name        TEXT    NOT NULL,
  mode        TEXT    NOT NULL DEFAULT 'reality', -- reality | tls
  server_json TEXT    NOT NULL DEFAULT '',        -- encrypted sing-box tls block
  client_json TEXT    NOT NULL DEFAULT '',        -- client params for links
  created_at  INTEGER NOT NULL,
  updated_at  INTEGER NOT NULL
);

-- sing-box server inbounds owned by 轻舟. options holds extra inbound fields
-- (transport, congestion_control, masquerade, ...) as JSON. A self_built node's
-- inbound_tag links to sb_inbounds.tag, so grouping/subscription keep working.
CREATE TABLE IF NOT EXISTS sb_inbounds (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  type        TEXT    NOT NULL,                   -- vless | hysteria2 | tuic | trojan | vmess
  tag         TEXT    NOT NULL UNIQUE,
  listen      TEXT    NOT NULL DEFAULT '::',
  listen_port INTEGER NOT NULL,
  tls_id      INTEGER NOT NULL DEFAULT 0,         -- -> sb_tls.id (0 = none)
  options     TEXT    NOT NULL DEFAULT '{}',      -- extra inbound fields (JSON)
  enabled     INTEGER NOT NULL DEFAULT 1,
  sort_order  INTEGER NOT NULL DEFAULT 0,
  created_at  INTEGER NOT NULL,
  updated_at  INTEGER NOT NULL
);

-- A "bucket" = an independently-metered unit a user holds: either a purchased
-- subscription plan or the shared traffic-package pool. Each bucket has its OWN
-- sing-box identity (client_name/uuid/secret) so sing-box's per-identity traffic
-- stats give per-bucket usage, its own quota, and its own expiry. Replaces the
-- single users.current_plan_id/traffic_limit/expiry_at model so a user can hold
-- several plans that expire and run out independently.
--   kind='plan': package_id → plan_groups; has expiry. kind='pool': package_id=0,
--   no expiry, covers the union of the user's plan groups + free group, drained
--   last. traffic_limit 0 = unlimited (plans only); pool with 0 limit is inert.
CREATE TABLE IF NOT EXISTS user_plans (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id        INTEGER NOT NULL,
  kind           TEXT    NOT NULL DEFAULT 'plan',  -- plan | pool
  package_id     INTEGER NOT NULL DEFAULT 0,
  name           TEXT    NOT NULL DEFAULT '',       -- snapshot, survives pkg delete
  client_name    TEXT    NOT NULL,                  -- sing-box stats identity (unique)
  client_uuid    TEXT    NOT NULL DEFAULT '',
  client_secret  TEXT    NOT NULL DEFAULT '',
  traffic_limit  INTEGER NOT NULL DEFAULT 0,
  used_up        INTEGER NOT NULL DEFAULT 0,
  used_down      INTEGER NOT NULL DEFAULT 0,
  expiry_at      INTEGER NOT NULL DEFAULT 0,        -- 0 = never
  last_online_at INTEGER NOT NULL DEFAULT 0,
  order_id       INTEGER NOT NULL DEFAULT 0,
  created_at     INTEGER NOT NULL,
  updated_at     INTEGER NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_user_plans_client ON user_plans(client_name);
CREATE INDEX IF NOT EXISTS idx_user_plans_user ON user_plans(user_id);

-- Per-user traffic time-series, one row per stats poll that saw traffic. Feeds
-- the daily charts in the native sing-box era (sing-box kept its own stat table);
-- pruned to a rolling window. up/down are per-poll deltas, not cumulative.
CREATE TABLE IF NOT EXISTS traffic_samples (
  user_id INTEGER NOT NULL,
  ts      INTEGER NOT NULL,
  up      INTEGER NOT NULL DEFAULT 0,
  down    INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_traffic_samples_user_ts ON traffic_samples(user_id, ts);
CREATE INDEX IF NOT EXISTS idx_traffic_samples_ts ON traffic_samples(ts);

CREATE TABLE IF NOT EXISTS servers (
  id              INTEGER PRIMARY KEY AUTOINCREMENT,
  name            TEXT    NOT NULL,
  host            TEXT    NOT NULL,
  port            INTEGER NOT NULL DEFAULT 22,
  ssh_user        TEXT    NOT NULL DEFAULT 'root',
  ssh_key         TEXT    NOT NULL DEFAULT '',
  ssh_key_pass    TEXT    NOT NULL DEFAULT '',
  config_path     TEXT    NOT NULL DEFAULT '/etc/sing-box/config.json',
  systemd_unit    TEXT    NOT NULL DEFAULT 'sing-box',
  v2ray_listen    TEXT    NOT NULL DEFAULT '127.0.0.1:18080',
  sing_box_bin    TEXT    NOT NULL DEFAULT '',
  enabled         INTEGER NOT NULL DEFAULT 1,
  status          TEXT    NOT NULL DEFAULT 'unknown',
  last_seen       INTEGER NOT NULL DEFAULT 0,
  created_at      INTEGER NOT NULL,
  updated_at      INTEGER NOT NULL
);

-- ===== Monitor probe (轻舟探针) =====
-- Per-server system metrics time-series, one row per agent report.
-- Pruned to a rolling window (default 30 days).
CREATE TABLE IF NOT EXISTS server_metrics (
  id              INTEGER PRIMARY KEY AUTOINCREMENT,
  server_id       INTEGER NOT NULL,
  ts              INTEGER NOT NULL,
  cpu_percent     REAL    NOT NULL DEFAULT 0,
  mem_used        INTEGER NOT NULL DEFAULT 0,
  mem_total       INTEGER NOT NULL DEFAULT 0,
  swap_used       INTEGER NOT NULL DEFAULT 0,
  swap_total      INTEGER NOT NULL DEFAULT 0,
  disk_used       INTEGER NOT NULL DEFAULT 0,
  disk_total      INTEGER NOT NULL DEFAULT 0,
  net_rx          INTEGER NOT NULL DEFAULT 0,
  net_tx          INTEGER NOT NULL DEFAULT 0,
  load1           REAL    NOT NULL DEFAULT 0,
  load5           REAL    NOT NULL DEFAULT 0,
  load15          REAL    NOT NULL DEFAULT 0,
  tcp_connections INTEGER NOT NULL DEFAULT 0,
  process_count   INTEGER NOT NULL DEFAULT 0,
  uptime          INTEGER NOT NULL DEFAULT 0,
  hostname        TEXT    NOT NULL DEFAULT '',
  platform        TEXT    NOT NULL DEFAULT '',
  kernel          TEXT    NOT NULL DEFAULT '',
  arch            TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_metrics_server_ts ON server_metrics(server_id, ts);
-- Standalone ts index: the composite (server_id, ts) can't serve queries that
-- filter on ts alone — the hourly PruneMetrics (WHERE ts<?) and the unauthenticated
-- heatmap/sparkline endpoints (ListAllMetricsSince, WHERE ts>=?) would otherwise
-- full-scan this, the fastest-growing table.
CREATE INDEX IF NOT EXISTS idx_metrics_ts ON server_metrics(ts);

-- Server alerts: offline, high_cpu, high_mem, disk_full, expiring, expired.
CREATE TABLE IF NOT EXISTS server_alerts (
  id        INTEGER PRIMARY KEY AUTOINCREMENT,
  server_id INTEGER NOT NULL,
  type      TEXT    NOT NULL,
  message   TEXT    NOT NULL,
  ts        INTEGER NOT NULL,
  read      INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_alerts_server ON server_alerts(server_id, ts);
`

func (s *Store) Migrate() error {
	if _, err := s.db.Exec(schema); err != nil {
		return err
	}
	// Additive column migrations for DBs created before these columns existed.
	// Errors (e.g. "duplicate column name") are expected on up-to-date DBs.
	for _, stmt := range []string{
		`ALTER TABLE announcements ADD COLUMN start_at INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE announcements ADD COLUMN end_at INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE node_sources ADD COLUMN group_ids TEXT NOT NULL DEFAULT ''`,
		// Shop selling-point bullets, stored as a JSON array of strings.
		`ALTER TABLE packages ADD COLUMN highlights TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE sb_inbounds ADD COLUMN server_id INTEGER NOT NULL DEFAULT 0`,
		// Relay chaining: an inbound with upstream_inbound_id != 0 forwards its
		// traffic to that landing inbound instead of exiting directly. relay_secret
		// is a landing inbound's own auth secret (lazily generated) from which the
		// relay credential is derived.
		`ALTER TABLE sb_inbounds ADD COLUMN upstream_inbound_id INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE sb_inbounds ADD COLUMN relay_secret TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE servers ADD COLUMN ssh_password TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE sb_tls ADD COLUMN server_id INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE users ADD COLUMN last_online_at INTEGER NOT NULL DEFAULT 0`,
		// Rename legacy columns to neutral names on DBs created before the
		// rename. Errors ("no such column") are expected on fresh/up-to-date DBs
		// where CREATE TABLE already used the new names.
		`ALTER TABLE users RENAME COLUMN sui_client_id TO client_id`,
		`ALTER TABLE users RENAME COLUMN sui_client_name TO client_name`,
		`ALTER TABLE users RENAME COLUMN sui_client_uuid TO client_uuid`,
		`ALTER TABLE users RENAME COLUMN sui_client_secret TO client_secret`,
		`ALTER TABLE nodes RENAME COLUMN sui_inbound_tag TO inbound_tag`,
		`DROP INDEX IF EXISTS idx_users_sui_client_name`,
		// Hot-path indexes. client_name backs AddUsageByClientName's
		// per-user UPDATE on every stats poll and UsersWithClient; source_id
		// backs ReplaceSourceNodes' delete-by-source; server_id backs the
		// per-server inbound filter.
		`CREATE INDEX IF NOT EXISTS idx_users_client_name ON users(client_name)`,
		`CREATE INDEX IF NOT EXISTS idx_nodes_source ON nodes(source_id)`,
		`CREATE INDEX IF NOT EXISTS idx_sb_inbounds_server ON sb_inbounds(server_id)`,
		// Monitor probe: extend servers table with probe/asset fields.
		`ALTER TABLE servers ADD COLUMN probe_enabled INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE servers ADD COLUMN probe_token TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE servers ADD COLUMN expiry_date INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE servers ADD COLUMN provider TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE servers ADD COLUMN location TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE servers ADD COLUMN spec TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE servers ADD COLUMN price REAL NOT NULL DEFAULT 0`,
		`ALTER TABLE servers ADD COLUMN notes TEXT NOT NULL DEFAULT ''`,
		// Pinned SSH host key (authorized_keys line) for TOFU verification, so the
		// panel doesn't blindly trust any host key on connect (MITM → root RCE).
		`ALTER TABLE servers ADD COLUMN host_key TEXT NOT NULL DEFAULT ''`,
		// Lookup index for the probe token: the token itself is now encrypted at
		// rest, so the report endpoint matches on this SHA-256 hash instead.
		`ALTER TABLE servers ADD COLUMN probe_token_hash TEXT NOT NULL DEFAULT ''`,
		// Prorated refunds: record how much was actually refunded on each order so
		// admin reporting and idempotent re-reads reflect the real (possibly partial)
		// amount instead of the original price. refund_ratio is the applied fraction
		// (0..1); refunded_traffic is the unused quota clawed back (audit).
		`ALTER TABLE orders ADD COLUMN refunded_points INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE orders ADD COLUMN refunded_at INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE orders ADD COLUMN refund_ratio REAL NOT NULL DEFAULT 0`,
		`ALTER TABLE orders ADD COLUMN refunded_traffic INTEGER NOT NULL DEFAULT 0`,
		// Per-bucket custom credentials for mixed (HTTP/SOCKS5) proxy inbounds: a
		// user-chosen username/password (proxy-only account, unrelated to login) that
		// replaces client_name/client_secret ONLY for mixed inbounds, with its own
		// expiry (0 = permanent). Empty proxy_username → fall back to client_name, so
		// existing buckets keep working. proxy_username is an additional sing-box
		// stats identity for the bucket, so AddBucketUsage matches it too.
		// Purchase idempotency: a client key (per purchase intent) so a network retry
		// after a lost response returns the same order instead of double-charging.
		// The partial unique index only constrains non-empty keys, so legacy/keyless
		// orders (and admin comps) are unaffected.
		`ALTER TABLE orders ADD COLUMN idempotency_key TEXT NOT NULL DEFAULT ''`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_orders_idem ON orders(user_id, idempotency_key) WHERE idempotency_key <> ''`,
		`ALTER TABLE user_plans ADD COLUMN proxy_username TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE user_plans ADD COLUMN proxy_password TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE user_plans ADD COLUMN proxy_expires_at INTEGER NOT NULL DEFAULT 0`,
		// A proxy_username must be globally unique (it becomes a stats identity);
		// partial index so the many empty defaults don't collide.
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_user_plans_proxy_username ON user_plans(proxy_username) WHERE proxy_username <> ''`,
	} {
		if _, err := s.db.Exec(stmt); err != nil {
			// Benign on an up-to-date DB: the column already exists (ADD COLUMN) or
			// was already renamed / never existed (RENAME COLUMN). Anything else — a
			// disk error, or a typo'd statement that never lands — must be surfaced,
			// not swallowed, or a required column shows up much later as a confusing
			// scan failure far from the cause.
			msg := err.Error()
			if strings.Contains(msg, "duplicate column name") ||
				strings.Contains(msg, "no such column") ||
				strings.Contains(msg, "no such table") {
				continue
			}
			log.Printf("migrate: statement failed (continuing): %q: %v", stmt, err)
		}
	}
	// Backfill probe_token_hash for existing (plaintext) tokens so hash-based
	// lookup keeps working after the upgrade. Idempotent (skips rows already set).
	if err := s.backfillProbeTokenHash(); err != nil {
		return err
	}
	// Seed the bucket model from legacy single-plan columns (idempotent).
	if err := s.backfillUserPlans(); err != nil {
		return err
	}
	// Collapse duplicate plan buckets left by pre-renewal repurchases (idempotent).
	if err := s.mergeDuplicatePlanBuckets(); err != nil {
		return err
	}
	// Give every existing provisioned user a free bucket (idempotent). This is
	// required, not cosmetic: the pool no longer covers the free group, so an
	// account without a free bucket would lose free-node access entirely.
	return s.backfillFreeBuckets()
}

// backfillFreeBuckets creates the free-group bucket for users provisioned before
// free traffic was split off the pool. Only users who already have a bucket are
// touched — an unprovisioned account gets its free bucket at provision time, and
// synthesising an identity for one here would put a user in the sing-box config
// who was never meant to be there.
func (s *Store) backfillFreeBuckets() error {
	rows, err := s.db.Query(`SELECT u.id, u.username FROM users u
		WHERE EXISTS (SELECT 1 FROM user_plans p WHERE p.user_id = u.id)
		  AND NOT EXISTS (SELECT 1 FROM user_plans p WHERE p.user_id = u.id AND p.kind = ?)`, KindFree)
	if err != nil {
		return err
	}
	type row struct {
		id   int64
		name string
	}
	var todo []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.name); err != nil {
			rows.Close()
			return err
		}
		todo = append(todo, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, r := range todo {
		if err := s.EnsureFreeBucket(r.id, r.name); err != nil {
			return err
		}
	}
	if len(todo) > 0 {
		log.Printf("migrate: created %d free-traffic buckets", len(todo))
	}
	return nil
}

// backfillProbeTokenHash computes probe_token_hash from the stored token for any
// probe-enabled server missing it (legacy rows whose token predates encryption).
func (s *Store) backfillProbeTokenHash() error {
	rows, err := s.db.Query(`SELECT id, probe_token FROM servers WHERE probe_token != '' AND probe_token_hash = ''`)
	if err != nil {
		return err
	}
	type row struct {
		id  int64
		tok string
	}
	var todo []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.tok); err != nil {
			rows.Close()
			return err
		}
		todo = append(todo, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, r := range todo {
		if _, err := s.db.Exec(`UPDATE servers SET probe_token_hash=? WHERE id=?`, hashProbeToken(r.tok), r.id); err != nil {
			return err
		}
	}
	return nil
}
