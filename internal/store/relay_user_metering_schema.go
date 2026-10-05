package store

// A relay user's owner and physical link never change. Retained disabled/old
// generations still identify delayed observations after a topology or user edit.
const relayUserMeteringSchema = `
CREATE TABLE relay_metering_users (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 link_id INTEGER NOT NULL, user_id INTEGER NOT NULL CHECK(user_id>0),
 generation INTEGER NOT NULL, identity_name TEXT NOT NULL UNIQUE,
 credential TEXT NOT NULL, enabled INTEGER NOT NULL DEFAULT 1,
 state TEXT NOT NULL DEFAULT 'prepared', created_at INTEGER NOT NULL,
 accepted_at INTEGER NOT NULL DEFAULT 0, activated_at INTEGER NOT NULL DEFAULT 0,
 source_names TEXT NOT NULL DEFAULT '[]',
 UNIQUE(link_id,user_id,generation)
);
CREATE INDEX idx_relay_metering_users_link ON relay_metering_users(link_id,enabled);
`
