-- oshiete schema.
--
-- No raw IP and no User-Agent is ever stored. `visitor` is an 8-byte HMAC of
-- (normalised IP prefix, User-Agent, site) keyed by OSHIETE_SECRET, `session`
-- is an HMAC of (visitor, 30-minute bucket). Both are 16 hex characters.

CREATE TABLE IF NOT EXISTS events (
  id       INTEGER PRIMARY KEY,
  ts       INTEGER NOT NULL,          -- unix seconds, server clock
  site     TEXT    NOT NULL,          -- 'blog'
  path     TEXT    NOT NULL,          -- '/posts/hello', query string stripped
  ref      TEXT,                      -- referrer host, NULL when direct/same-site
  country  TEXT,                      -- ISO 3166-1 alpha-2 from CF, never an IP
  device   TEXT,                      -- mobile | tablet | desktop | bot
  browser  TEXT,                      -- chrome | safari | firefox | edge | opera | other
  os       TEXT,                      -- windows | macos | linux | android | ios | other
  width    INTEGER,                   -- viewport width in CSS px
  lang     TEXT,                      -- 'ja'
  visitor  TEXT    NOT NULL,
  session  TEXT    NOT NULL,
  is_bot   INTEGER NOT NULL DEFAULT 0
);

-- Period queries and the TUI's activity chart.
CREATE INDEX IF NOT EXISTS events_site_ts    ON events(site, ts);
-- "follow this one visitor" — required, otherwise every drill-down scans.
CREATE INDEX IF NOT EXISTS events_visitor_ts ON events(visitor, ts);
-- Session rollups.
CREATE INDEX IF NOT EXISTS events_session    ON events(session);
-- The sync cursor: `WHERE site = ? AND id > ? ORDER BY id`.
CREATE INDEX IF NOT EXISTS events_site_id    ON events(site, id);

-- Denormalised visitor dimension. Everything here is derivable from `events`,
-- but recomputing first/last seen on every render does not scale past a few
-- hundred thousand rows.
CREATE TABLE IF NOT EXISTS visitors (
  visitor      TEXT PRIMARY KEY,
  site         TEXT    NOT NULL,
  first_seen   INTEGER NOT NULL,
  last_seen    INTEGER NOT NULL,
  sessions     INTEGER NOT NULL DEFAULT 1,
  last_session TEXT,
  country      TEXT,
  device       TEXT
);

CREATE INDEX IF NOT EXISTS visitors_site_last ON visitors(site, last_seen);

-- Hourly rollup. Views only: unique counts do not sum across buckets, so a
-- column of per-hour uniques could not be aggregated into a day or a week
-- without over-counting. Nothing writes this yet; it is here so that pruning
-- raw events later does not lose the long tail.
CREATE TABLE IF NOT EXISTS rollup (
  hour  INTEGER NOT NULL,
  site  TEXT    NOT NULL,
  path  TEXT    NOT NULL,
  views INTEGER NOT NULL,
  PRIMARY KEY (hour, site, path)
);
