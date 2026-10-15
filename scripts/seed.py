#!/usr/bin/env python3
"""Populate a mirror with synthetic traffic, so the dashboard can be seen
without waiting for real visitors.

The collector is not touched: this writes straight into a SQLite mirror. Point
the TUI at the file you generate and the live mirror stays clean.

    python3 scripts/seed.py ~/oshiete/data/demo.db
    OSHIETE_DB=~/oshiete/data/demo.db ./tui/oshiete

Delete the file when you are done with it.
"""

from __future__ import annotations

import argparse
import hashlib
import os
import random
import sqlite3
import time

SITES = {
    "blog": {
        "paths": [
            "/",
            "/posts/hello-world",
            "/posts/sqlite-wal",
            "/posts/tui-in-go",
            "/posts/cloudflare-d1",
            "/posts/oshiete",
            "/about",
            "/archive",
        ],
        "weights": [30, 18, 12, 14, 11, 5, 6, 4],
        "share": 0.85,
    },
    "docs": {
        "paths": ["/", "/install", "/config", "/api", "/api/events", "/changelog"],
        "weights": [25, 20, 18, 16, 13, 8],
        "share": 0.15,
    },
}

REFS = [
    "news.ycombinator.com",
    "google",
    "x.com",
    "reddit.com",
    "github.com",
    "bing",
    None,
    None,
    None,
    None,
]

# country, weight, language
COUNTRIES = [
    ("JP", 46, "ja"),
    ("US", 16, "en"),
    ("DE", 6, "de"),
    ("GB", 5, "en"),
    ("FR", 4, "fr"),
    ("KR", 4, "ko"),
    ("TW", 4, "zh"),
    ("IN", 5, "en"),
    ("CA", 4, "en"),
    ("NL", 3, "nl"),
    ("AU", 3, "en"),
]

# device, browser, os, typical viewport width
PROFILES = [
    ("desktop", "chrome", "windows", 1920, 30),
    ("desktop", "safari", "macos", 1440, 14),
    ("desktop", "firefox", "linux", 1600, 8),
    ("desktop", "edge", "windows", 1536, 7),
    ("mobile", "safari", "ios", 390, 24),
    ("mobile", "chrome", "android", 412, 14),
    ("tablet", "safari", "ios", 834, 3),
]

SCHEMA = """
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
"""


def pick(rng: random.Random, weighted):
    total = sum(w for _, w in weighted)
    r = rng.uniform(0, total)
    upto = 0.0
    for value, w in weighted:
        upto += w
        if r <= upto:
            return value
    return weighted[-1][0]


def digest(*parts: str, n: int = 16) -> str:
    h = hashlib.sha256("\x00".join(parts).encode()).hexdigest()
    return h[:n]


def main() -> None:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("db", help="mirror file to create")
    ap.add_argument("--days", type=int, default=30)
    ap.add_argument("--visitors", type=int, default=220)
    ap.add_argument("--seed", type=int, default=7)
    ap.add_argument("--force", action="store_true", help="overwrite an existing file")
    args = ap.parse_args()

    if os.path.exists(args.db) and not args.force:
        raise SystemExit(f"{args.db} already exists; pass --force to overwrite")

    path = os.path.abspath(args.db)
    os.makedirs(os.path.dirname(path), mode=0o700, exist_ok=True)
    if os.path.exists(path):
        os.remove(path)

    rng = random.Random(args.seed)
    now = int(time.time())
    start = now - args.days * 86400

    site_names = list(SITES)
    site_weights = [(s, int(SITES[s]["share"] * 1000)) for s in site_names]

    rows: list[tuple] = []

    for v in range(args.visitors):
        country, lang = pick(rng, [((c, l), w) for c, w, l in COUNTRIES])
        device, browser, osname, width = pick(rng, [(p[:4], p[4]) for p in PROFILES])

        # A visitor arrives once, and some of them come back.
        returning = rng.random() < 0.28
        n_sessions = 1
        while returning and rng.random() < 0.45 and n_sessions < 9:
            n_sessions += 1

        first = rng.randint(start, now - 600)
        for s in range(n_sessions):
            # Later sessions fall after the first, roughly weekly.
            if s == 0:
                t0 = first
            else:
                t0 = first + s * rng.randint(6 * 3600, 9 * 86400) + rng.randint(0, 3600)
                if t0 >= now:
                    break

            site = pick(rng, site_weights)
            visitor = digest("visitor", str(v), site)
            session = digest("session", visitor, str(t0 // 1800))

            ref = rng.choice(REFS) if s == 0 else None

            views = 1
            while rng.random() < 0.55 and views < 7:
                views += 1

            t = t0
            for i in range(views):
                spec = SITES[site]
                p = pick(rng, list(zip(spec["paths"], spec["weights"])))
                rows.append(
                    (
                        t,
                        site,
                        p,
                        ref if i == 0 else None,
                        country,
                        device,
                        browser,
                        osname,
                        width + rng.randint(-20, 20),
                        lang,
                        visitor,
                        session,
                        0,
                    )
                )
                t += rng.randint(20, 900)

    # A little bot traffic, so the filter is visibly doing something.
    for _ in range(int(len(rows) * 0.04)):
        site = rng.choice(site_names)
        rows.append(
            (
                rng.randint(start, now),
                site,
                rng.choice(SITES[site]["paths"]),
                None,
                "US",
                "bot",
                "other",
                "other",
                0,
                "en",
                digest("bot", str(rng.random())),
                digest("botsession", str(rng.random())),
                1,
            )
        )

    rows.sort(key=lambda r: r[0])

    con = sqlite3.connect(path)
    con.executescript(SCHEMA)
    con.executemany(
        """INSERT INTO events
             (ts, site, path, ref, country, device, browser, os, width, lang, visitor, session, is_bot)
           VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)""",
        rows,
    )
    con.commit()
    con.close()

    humans = sum(1 for r in rows if r[12] == 0)
    print(f"{path}: {len(rows)} events ({humans} human, {len(rows) - humans} bot), {args.visitors} visitors")


if __name__ == "__main__":
    main()
