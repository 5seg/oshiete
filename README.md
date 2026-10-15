# oshiete

Anonymous pageview tracking that you read in a terminal.

A ~5 KB Worker accepts one pageview per page load, reduces the visitor to a
pseudonym, and drops the IP and the User-Agent on the spot. A Go TUI keeps a
local SQLite mirror and draws the whole dashboard from local SQL, so redraws
are instant and the collector is not asked anything while you browse.

```
oshiete  blog  30d  (2 sites)                                      synced 2s ago
[概要]  ライブ   ページ   訪問者   比較

  閲覧 469     訪問者 199     新規 93%     中央値 48s     再訪 13

  ▄▂▂▁▂▂▄▄▁▁▂▁▄▃▄▄▁▄▂▂▁▂▃▁▁▂▂▂▁▁▁▂▁▂▃▃▂▁▂▄▂▂▅▃▄▁▂▃▄▃▃▄▂▅▁▄▁▂▁▁▃▂▁▃▅▁▁▃▂▂▂▃▁▁▁▃▂▁▁▆▁▂▂▆▃▆▆▄▄▁▃▁▁▃▄▁▁▃▁
  30d の推移

ライブ（5分）                    上位ページ
  —                                /                      141 ██████████████████████████
                                   /posts/hello-world      82 ███████████████
                                   /posts/tui-in-go        62 ███████████
流入元                             /posts/sqlite-wal       57 ██████████
  (none)                 338 ███   /posts/cloudflare-d1    51 █████████
  google                  24 █     /posts/oshiete          29 █████
  github.com              23 █     /about                  19 ███
```

## What is stored

| | |
|---|---|
| IP address | never written, never logged. Reduced to a `/64` (IPv6) or kept whole (IPv4), hashed, discarded |
| User-Agent | never written. Parsed into `device`, `browser`, `os` and discarded |
| Cookies, `localStorage`, `sessionStorage` | none. The beacon keeps no state on the device |
| Fingerprinting | none. No canvas, no fonts, no screen geometry beyond the viewport width |
| Third parties | none. Your Worker, your D1 |
| Country | the two-letter code from Cloudflare, resolved at ingest |

What *is* stored per pageview: a timestamp, the site, the path, the referrer
host, the country, coarse device and browser buckets, the viewport width, the
language, and two 16-character pseudonyms described below.

### The two pseudonyms

```
visitor = HMAC(OSHIETE_SECRET, ip-prefix ‖ user-agent ‖ site)[:8]
session = HMAC(OSHIETE_SECRET, visitor ‖ 30-minute-bucket)[:8]
```

`visitor` is stable, which is what makes "returning visitor", "first seen" and
a visitor's trail across days work at all. It is a pseudonym, not an identity:
there is no IP in the database to compare it against, the hash is keyed by a
secret that only lives in the Worker, and the site name is part of the input so
the same person on two of your sites is two unrelated pseudonyms.

Rotating the key is the dial on how long visitors stay linked:

```sh
# a fresh secret makes every visitor a stranger
$EDITOR worker/.secrets.json      # new OSHIETE_SECRET
make deploy
```

Rotating it also resets `visitor` for everyone, so returning-visitor counts
restart. The interval is yours to pick; nothing in the code assumes one.

### What "one visitor" actually means

`visitor` is a function of the IP prefix and the User-Agent, so:

- an office or cafe where everyone shares one NAT address and one browser build
  collapses into a single visitor;
- one person on a phone and a laptop, or moving between wifi and mobile data, is
  two or more visitors.

This is the same limit every cookieless analytics tool has. Removing it requires
a persistent identifier on the device, which is the thing this design gives up.

## How a pageview flows

```
browser ──POST /e──▶ Worker ──▶ D1
                       │
                       └─ ip + ua → HMAC → pseudonyms → discarded

tui ──GET /sites──▶ Worker        which sites exist
    ──GET /since──▶ Worker        events with id > cursor
         │
         └─▶ ~/oshiete/data/mirror.db (SQLite)  ──▶ every view is local SQL
```

The sync is incremental on `events.id` and idempotent, so a dropped connection
costs nothing and the cursor only advances after a commit.

## Install

### 1. The collector

```sh
git clone https://github.com/5seg/oshiete && cd oshiete/worker
npm install

./scripts/cf d1 create --name oshiete          # note the uuid
# put it in cloudflare.config.ts, then:
./scripts/cf d1 migrations apply <uuid> --dir migrations
```

Generate the two secrets and deploy. `OSHIETE_SECRET` keys the pseudonyms;
`SYNC_TOKEN` is what the TUI authenticates with.

```sh
cat > worker/.secrets.json <<EOF
{
  "OSHIETE_SECRET": "$(openssl rand -hex 32)",
  "SYNC_TOKEN": "$(openssl rand -base64 32)"
}
EOF
chmod 600 worker/.secrets.json

mkdir -p ~/oshiete/data && chmod 700 ~/oshiete/data
jq -r .SYNC_TOKEN worker/.secrets.json > ~/oshiete/data/token
chmod 600 ~/oshiete/data/token

make deploy
```

`cloudflare.config.ts` sets `domains: ["o.5seg.top"]`; change it to your own
hostname, or delete the line to use the `*.workers.dev` address.

`worker/.secrets.json` is gitignored and mode 600. Keep it: if `OSHIETE_SECRET`
is lost, a redeploy with a new one unlinks every visitor, and there is no way to
recover the old pseudonyms.

### 2. The beacon

```html
<script defer src="https://o.5seg.top/b.js" data-site="blog"></script>
```

One line, on every page. `data-site` is the name that shows up in the dashboard;
without it the hostname is used, with `www.` removed and dots turned into dashes.
The beacon also reports client-side route changes, so static sites that use view
transitions or an SPA router are counted correctly.

It skips a pageview when Do Not Track or Global Privacy Control is set, or when
`navigator.webdriver` is present.

### 3. The dashboard

```sh
make tui
./tui/oshiete          # syncs in the background, refreshes every 3s
./tui/oshiete sync     # one-shot sync
./tui/oshiete sites    # list sites in the mirror
```

| key | |
|---|---|
| `1`–`5` | 概要 / ライブ / ページ / 訪問者 / 比較 |
| `tab` | next view |
| `t` | period: 1h, 24h, 7d, 30d |
| `s` | next site |
| `/` | filter by path |
| `↑` `↓` | move; `enter` opens a visitor's full trail |
| `r` | resync now |
| `q` | quit |

| environment | |
|---|---|
| `OSHIETE_ENDPOINT` | collector URL, default `https://o.5seg.top` |
| `OSHIETE_TOKEN` | sync token, default `~/oshiete/data/token` |
| `OSHIETE_DB` | mirror path, default `~/oshiete/data/mirror.db` |

To see the dashboard before you have traffic:

```sh
make seed
OSHIETE_DB=$HOME/oshiete/data/demo.db ./tui/oshiete
```

## Multiple sites

Each `data-site` value is its own dimension. `s` cycles between them, and the
pseudonym includes the site name, so a reader of two of your sites appears as two
unrelated visitors. If you would rather link them, drop `site` from the `mac(...)`
call in `worker/src/index.ts` and accept the privacy cost.

## Operational notes

**`scripts/cf` exists for a reason.** workerd ships as a glibc binary and
segfaults under musl, which breaks `cf build`, `cf dev` and `cf workers types` on
Alpine. The wrapper runs the CLI in a glibc container with the project and the
CLI's own auth config bind-mounted at the same paths, so the commands and config
files are unchanged. `make deploy`, `make dev` and `make types` go through it.

**Spam.** `POST /e` is world-writable — the site name is in your page source, so
no real authentication is possible. Set `ALLOWED_ORIGINS` in
`cloudflare.config.ts` to a comma-separated list of origins to reject posts from
anywhere else, and add a Cloudflare rate-limiting rule on `/e` if it becomes a
problem. Bot user-agents are additionally recorded with `is_bot = 1` and hidden
from every view rather than dropped, so you can tell a filter bug from quiet days.

**Retention.** Nothing is pruned. At personal-blog volumes a year of pageviews is
a few megabytes, and the free D1 tier is 5 GB. There is an empty `rollup` table
in the schema for hourly view counts if you later want to drop raw events and keep
the long tail; nothing writes it yet.

**Untested on high volume.** The queries are indexed for the shapes the TUI uses
(`(site, ts)` for periods, `(visitor, ts)` for trails), but the mirror has only
been exercised against seeded traffic in the thousands of rows.

## Layout

```
worker/          Cloudflare Worker + D1 schema
  src/index.ts   ingest, sync feed, beacon serving
  src/beacon.js  the beacon, served at /b.js
  migrations/
tui/             Go dashboard (bubbletea + modernc sqlite, no cgo)
scripts/cf       run the cf CLI in a glibc container
scripts/seed.py  synthetic traffic into a throwaway mirror
```
