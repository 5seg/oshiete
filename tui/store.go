package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	_ "modernc.org/sqlite"
)

// Store is the local mirror.
//
// It holds events and the sync cursor and nothing else. Every aggregate the UI
// shows is derived from `events` at query time, so there is no second copy of
// the truth to keep consistent with the collector.
type Store struct{ db *sql.DB }

const mirrorSchema = `
CREATE TABLE IF NOT EXISTS events (
  id       INTEGER PRIMARY KEY,
  ts       INTEGER NOT NULL,
  site     TEXT    NOT NULL,
  path     TEXT    NOT NULL,
  ref      TEXT,
  country  TEXT,
  device   TEXT,
  browser  TEXT,
  os       TEXT,
  width    INTEGER,
  lang     TEXT,
  visitor  TEXT    NOT NULL,
  session  TEXT    NOT NULL,
  is_bot   INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS events_site_ts    ON events(site, ts);
CREATE INDEX IF NOT EXISTS events_visitor_ts ON events(visitor, ts);
CREATE INDEX IF NOT EXISTS events_session    ON events(session);
CREATE INDEX IF NOT EXISTS events_site_id    ON events(site, id);

CREATE TABLE IF NOT EXISTS sync_state (
  site       TEXT PRIMARY KEY,
  cursor     INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
`

func OpenStore(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create mirror directory: %w", err)
	}
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open mirror: %w", err)
	}
	// WAL gives readers a consistent snapshot while the syncer writes, so reads
	// do not need to be serialised behind the one writer. More than one
	// connection is also load-bearing: a query that opens a second statement
	// while a cursor is live would deadlock against a limit of one.
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(4)
	if _, err := db.Exec(mirrorSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("initialise mirror: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// ---------------------------------------------------------------------------
// sync bookkeeping
// ---------------------------------------------------------------------------

func (s *Store) Cursor(site string) (int64, error) {
	var c int64
	err := s.db.QueryRow(`SELECT cursor FROM sync_state WHERE site = ?`, site).Scan(&c)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return c, err
}

func (s *Store) SetCursor(site string, cursor int64) error {
	_, err := s.db.Exec(
		`INSERT INTO sync_state (site, cursor, updated_at) VALUES (?, ?, ?)
		 ON CONFLICT(site) DO UPDATE SET cursor = excluded.cursor, updated_at = excluded.updated_at`,
		site, cursor, time.Now().Unix(),
	)
	return err
}

func (s *Store) SyncedAt(site string) time.Time {
	var at int64
	if err := s.db.QueryRow(`SELECT updated_at FROM sync_state WHERE site = ?`, site).Scan(&at); err != nil {
		return time.Time{}
	}
	return time.Unix(at, 0)
}

// ---------------------------------------------------------------------------
// shape of what the UI reads
// ---------------------------------------------------------------------------

type NameCount struct {
	Name string
	N    int
}

type Overview struct {
	Views      int
	Visitors   int
	New        int
	Bots       int
	MedianSess time.Duration
}

type LiveRow struct {
	Ts                 int64
	Visitor, Path, Ref string
	Country, Device    string
}

type PageRow struct {
	Path     string
	Views    int
	Visitors int
	Bounce   float64 // share of entries on this path whose session ended here
}

type VisitorRow struct {
	Visitor             string
	FirstSeen, LastSeen int64
	Sessions, Views     int
	Country, Device     string
	Entry               string
}

type TrailRow struct {
	Ts      int64
	Path    string
	Ref     string
	Country string
	Device  string
	Width   int
	Lang    string
	Session string
	IsBot   bool
}

// ---------------------------------------------------------------------------

// Sites lists the sites present in the mirror, most recently active first.
func (s *Store) Sites() ([]string, error) {
	rows, err := s.db.Query(`SELECT site, MAX(ts) m FROM events GROUP BY site ORDER BY m DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var site string
		var m int64
		if err := rows.Scan(&site, &m); err != nil {
			return nil, err
		}
		out = append(out, site)
	}
	return out, rows.Err()
}

func (s *Store) Overview(site string, from, to int64) (Overview, error) {
	var o Overview
	err := s.db.QueryRow(`
		SELECT COUNT(*),
		       COUNT(DISTINCT visitor),
		       COALESCE(SUM(is_bot), 0)
		  FROM events
		 WHERE site = ? AND ts >= ? AND ts < ?`,
		site, from, to,
	).Scan(&o.Views, &o.Visitors, &o.Bots)
	if err != nil {
		return o, err
	}

	// A visitor is "new" when this period contains their first ever event.
	err = s.db.QueryRow(`
		SELECT COUNT(*) FROM (
		  SELECT visitor FROM events
		   WHERE site = ? AND is_bot = 0
		   GROUP BY visitor
		  HAVING MIN(ts) >= ? AND MIN(ts) < ?
		)`, site, from, to,
	).Scan(&o.New)
	if err != nil {
		return o, err
	}

	o.MedianSess, err = s.medianSession(site, from, to)
	return o, err
}

// medianSession is the middle session length in the period, where a session
// runs from its first to its last event.
func (s *Store) medianSession(site string, from, to int64) (time.Duration, error) {
	rows, err := s.db.Query(`
		SELECT MAX(ts) - MIN(ts) AS d
		  FROM events
		 WHERE site = ? AND ts >= ? AND ts < ? AND is_bot = 0
		 GROUP BY session`, site, from, to)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	var ds []int64
	for rows.Next() {
		var d int64
		if err := rows.Scan(&d); err != nil {
			return 0, err
		}
		ds = append(ds, d)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(ds) == 0 {
		return 0, nil
	}
	sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
	return time.Duration(ds[len(ds)/2]) * time.Second, nil
}

// Buckets returns pageviews per bucket across [from, to), zero-filled.
func (s *Store) Buckets(site string, from, to int64, n int) ([]int64, error) {
	if n <= 0 {
		return nil, nil
	}
	size := (to - from) / int64(n)
	if size < 1 {
		size = 1
	}
	out := make([]int64, n)

	rows, err := s.db.Query(`
		SELECT (ts / ?) * ? AS bucket, COUNT(*)
		  FROM events
		 WHERE site = ? AND ts >= ? AND ts < ? AND is_bot = 0
		 GROUP BY bucket`, size, size, site, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var bucket, c int64
		if err := rows.Scan(&bucket, &c); err != nil {
			return nil, err
		}
		i := int((bucket - from) / size)
		if i >= 0 && i < n {
			out[i] += c
		}
	}
	return out, rows.Err()
}

func (s *Store) Live(site string, since int64, limit int) ([]LiveRow, error) {
	rows, err := s.db.Query(`
		SELECT ts, visitor, path, COALESCE(ref, ''), COALESCE(country, ''), COALESCE(device, '')
		  FROM events
		 WHERE site = ? AND ts >= ? AND is_bot = 0
		 ORDER BY ts DESC
		 LIMIT ?`, site, since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []LiveRow
	for rows.Next() {
		var r LiveRow
		if err := rows.Scan(&r.Ts, &r.Visitor, &r.Path, &r.Ref, &r.Country, &r.Device); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) Pages(site string, from, to int64, limit int, contains string) ([]PageRow, error) {
	// Bounce is the share of a path's pageviews that ended their session. It is
	// computed in the same statement rather than per row: a second query while
	// this cursor is open would need a second connection, and `bounce` would also
	// have to re-read the window once per path.
	rows, err := s.db.Query(`
		WITH base AS (
		  SELECT ts, session, path, visitor FROM events
		   WHERE site = ? AND ts >= ? AND ts < ? AND is_bot = 0
		),
		sess_end AS (SELECT session, MAX(ts) AS mx FROM base GROUP BY session),
		win AS (SELECT * FROM base WHERE (? = '' OR path LIKE '%' || ? || '%'))
		SELECT w.path,
		       COUNT(*)                  AS views,
		       COUNT(DISTINCT w.visitor)  AS visitors,
		       AVG(CASE WHEN w.ts = s.mx THEN 1.0 ELSE 0.0 END) AS bounce
		  FROM win w JOIN sess_end s ON s.session = w.session
		 GROUP BY w.path
		 ORDER BY views DESC
		 LIMIT ?`, site, from, to, contains, contains, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []PageRow
	for rows.Next() {
		var r PageRow
		if err := rows.Scan(&r.Path, &r.Views, &r.Visitors, &r.Bounce); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) Top(site, column string, from, to int64, limit int) ([]NameCount, error) {
	// `column` is chosen by the caller from a fixed set, never from user input.
	if column != "ref" && column != "country" && column != "device" && column != "browser" && column != "os" {
		return nil, fmt.Errorf("unsupported column %q", column)
	}
	rows, err := s.db.Query(fmt.Sprintf(`
		SELECT COALESCE(NULLIF(%s, ''), '(none)') AS name, COUNT(*) n
		  FROM events
		 WHERE site = ? AND ts >= ? AND ts < ? AND is_bot = 0
		 GROUP BY name
		 ORDER BY n DESC
		 LIMIT ?`, column), site, from, to, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []NameCount
	for rows.Next() {
		var r NameCount
		if err := rows.Scan(&r.Name, &r.N); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) Visitors(site string, from, to int64, limit int, contains string) ([]VisitorRow, error) {
	rows, err := s.db.Query(`
		WITH win AS (
		  SELECT * FROM events
		   WHERE site = ? AND ts >= ? AND ts < ? AND is_bot = 0
		     AND (? = '' OR path LIKE '%' || ? || '%')
		),
		first AS (
		  SELECT visitor, MIN(ts) AS all_first FROM events WHERE site = ? GROUP BY visitor
		)
		SELECT w.visitor,
		       f.all_first,
		       MAX(w.ts)             AS last_seen,
		       COUNT(DISTINCT w.session) AS sessions,
		       COUNT(*)              AS views,
		       COALESCE(MAX(w.country), '') AS country,
		       COALESCE(MAX(w.device), '')  AS device,
		       (SELECT path FROM events WHERE visitor = w.visitor AND site = ?
		         ORDER BY ts LIMIT 1) AS entry
		  FROM win w JOIN first f ON f.visitor = w.visitor
		 GROUP BY w.visitor
		 ORDER BY last_seen DESC
		 LIMIT ?`, site, from, to, contains, contains, site, site, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []VisitorRow
	for rows.Next() {
		var r VisitorRow
		if err := rows.Scan(&r.Visitor, &r.FirstSeen, &r.LastSeen, &r.Sessions, &r.Views, &r.Country, &r.Device, &r.Entry); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Trail is one visitor's full history: every pageview, across every session and
// across days, in order.
func (s *Store) Trail(visitor string) ([]TrailRow, error) {
	rows, err := s.db.Query(`
		SELECT ts, path, COALESCE(ref, ''), COALESCE(country, ''), COALESCE(device, ''),
		       COALESCE(width, 0), COALESCE(lang, ''), session, is_bot
		  FROM events
		 WHERE visitor = ?
		 ORDER BY ts
		 LIMIT 2000`, visitor)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []TrailRow
	for rows.Next() {
		var r TrailRow
		var bot int
		if err := rows.Scan(&r.Ts, &r.Path, &r.Ref, &r.Country, &r.Device, &r.Width, &r.Lang, &r.Session, &bot); err != nil {
			return nil, err
		}
		r.IsBot = bot != 0
		out = append(out, r)
	}
	return out, rows.Err()
}

func shortVisitor(v string) string {
	if len(v) <= 8 {
		return v
	}
	return v[:8]
}
