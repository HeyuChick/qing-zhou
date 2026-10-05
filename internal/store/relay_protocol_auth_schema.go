package store

// Append-only migration: old prepared/accepted maps do not invent proof of
// source credentials. PrepareRelayMetering records digests before the next
// source apply; until then an empty map cannot acknowledge a route as active.
const relayProtocolAuthSchema = `
ALTER TABLE relay_metering_users ADD COLUMN source_auth_hashes TEXT NOT NULL DEFAULT '{}';
UPDATE relay_metering_users SET state='accepted',activated_at=0 WHERE state='active';
UPDATE relay_metering_links SET state='accepted',activated_at=0
 WHERE state='active' AND EXISTS (SELECT 1 FROM relay_metering_users u
 WHERE u.link_id=relay_metering_links.id AND u.generation=relay_metering_links.generation AND u.enabled=1);
`
