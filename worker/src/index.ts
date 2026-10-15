/**
 * oshiete collector.
 *
 *   GET  /b.js     the beacon itself, served from here so there is one source
 *   POST /e        beacon ingest, world-writable by design
 *   GET  /since    incremental sync feed for the local mirror (bearer token)
 *   GET  /sites    site list and cursors, so the mirror can discover them
 *   GET  /health   liveness probe
 *
 * Anonymity model: the IP and the User-Agent are read from the request, reduced
 * to a pseudonym, and dropped. Neither is written to D1, logged, or returned.
 * There is no cookie, no client-side identifier, and no third party.
 */
import { env } from "cloudflare:workers";
import beaconSource from "./beacon.js?raw";

const V = 1;
const SESSION_BUCKET = 1800;
const MAX_BODY = 4096;
const MAX_LIMIT = 5000;
const DEFAULT_LIMIT = 1000;
const MAX_PATH = 512;
const MAX_REF = 190;

const SITE_RE = /^[a-z0-9][a-z0-9_-]{0,31}$/;
const LANG_RE = /^[A-Za-z][A-Za-z0-9-]{0,15}$/;

const BOT_RE =
  /bot\b|\bbots\b|crawl|spider|slurp|headless|phantomjs|curl\/|wget\/|python-requests|python-urllib|httpx\/|aiohttp|axios\/|node-fetch|undici|go-http-client|okhttp|java\/|libwww|facebookexternalhit|embedly|quora link preview|whatsapp\/|telegrambot|discordbot|preview|monitoring|uptime|pingdom/i;

// ---------------------------------------------------------------------------
// crypto
// ---------------------------------------------------------------------------

let keyPromise: Promise<CryptoKey> | null = null;

function masterKey(secret: string): Promise<CryptoKey> {
  keyPromise ??= crypto.subtle.importKey(
    "raw",
    new TextEncoder().encode(secret),
    { name: "HMAC", hash: "SHA-256" },
    false,
    ["sign"],
  );
  return keyPromise;
}

const HEX = Array.from({ length: 256 }, (_, i) => i.toString(16).padStart(2, "0"));

/**
 * Keyed MAC over NUL-joined parts. The label separates domains so the same
 * secret yields independent visitor and session spaces.
 */
async function mac(secret: string, label: string, parts: string[], bytes: number): Promise<string> {
  const msg = new TextEncoder().encode(`${label}\u0000${parts.join("\u0000")}`);
  const sig = new Uint8Array(await crypto.subtle.sign("HMAC", await masterKey(secret), msg));
  let out = "";
  for (let i = 0; i < bytes; i++) out += HEX[sig[i]];
  return out;
}

function timingSafeEqual(a: string, b: string): boolean {
  const x = new TextEncoder().encode(a);
  const y = new TextEncoder().encode(b);
  if (x.length !== y.length) return false;
  let diff = 0;
  for (let i = 0; i < x.length; i++) diff |= x[i] ^ y[i];
  return diff === 0;
}

// ---------------------------------------------------------------------------
// normalisation
// ---------------------------------------------------------------------------

/**
 * Reduce an address to the part that is stable for one subscriber line.
 *
 * IPv4 is kept whole. IPv6 is truncated to /64, because privacy extensions
 * (RFC 4941) rotate the low 64 bits daily and would otherwise split a single
 * visitor into several. IPv4-mapped IPv6 falls back to the embedded IPv4.
 */
export function ipPrefix(ip: string): string {
  if (!ip) return "?";
  if (!ip.includes(":")) return ip.toLowerCase();

  const mapped = ip.match(/(\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3})$/);
  if (mapped) return mapped[1];

  const bare = ip.split("%")[0];
  const [headStr, tailStr] = bare.split("::");
  const head = headStr ? headStr.split(":") : [];
  const tail = tailStr ? tailStr.split(":") : [];
  const fill = bare.includes("::") ? Math.max(0, 8 - head.length - tail.length) : 0;
  const full = [...head, ...Array<string>(fill).fill("0"), ...tail];
  if (full.length < 4) return "?";
  return full
    .slice(0, 4)
    .map((h) => h.padStart(4, "0").toLowerCase())
    .join(":");
}

export function normalisePath(raw: string): string {
  let p = (raw || "/").split("#")[0].split("?")[0];
  if (!p.startsWith("/")) p = `/${p}`;
  p = p.replace(/\/{2,}/g, "/").replace(/\/index\.html?$/i, "/");
  if (p.length > 1 && p.endsWith("/")) p = p.slice(0, -1);
  if (!p) p = "/";
  return p.slice(0, MAX_PATH);
}

export function normaliseRef(raw: string): string | null {
  if (!raw) return null;
  let r = raw.trim().toLowerCase();
  r = r.replace(/^[a-z]+:\/\//, "").split("/")[0].split("?")[0].split("@").pop() ?? "";
  r = r.replace(/:\d+$/, "");
  if (!r || !r.includes(".")) return null;
  return r.slice(0, MAX_REF);
}

export function parseUA(ua: string) {
  const bot = BOT_RE.test(ua);

  let device = "desktop";
  if (/ipad|tablet|playbook|silk/i.test(ua) || (/android/i.test(ua) && !/mobile/i.test(ua))) {
    device = "tablet";
  } else if (/mobi|iphone|ipod|android|blackberry|opera mini|iemobile/i.test(ua)) {
    device = "mobile";
  }
  if (bot) device = "bot";

  let browser = "other";
  if (/edg[ea]?\//i.test(ua)) browser = "edge";
  else if (/opr\/|opera/i.test(ua)) browser = "opera";
  else if (/firefox\/|fxios/i.test(ua)) browser = "firefox";
  else if (/chrome\/|crios/i.test(ua)) browser = "chrome";
  else if (/safari\//i.test(ua)) browser = "safari";

  let os = "other";
  if (/windows/i.test(ua)) os = "windows";
  else if (/android/i.test(ua)) os = "android";
  else if (/iphone|ipad|ipod|ios/i.test(ua)) os = "ios";
  else if (/mac os x|macintosh/i.test(ua)) os = "macos";
  else if (/linux|x11|cros/i.test(ua)) os = "linux";

  return { device, browser, os, bot };
}

// ---------------------------------------------------------------------------
// CORS
// ---------------------------------------------------------------------------

function corsHeaders(origin: string | null): Headers {
  const h = new Headers();
  h.set("access-control-allow-origin", origin ?? "*");
  h.set("vary", "Origin");
  return h;
}

function originAllowed(origin: string | null, list: string | undefined): boolean {
  const allow = (list ?? "")
    .split(",")
    .map((s) => s.trim().toLowerCase())
    .filter(Boolean);
  if (allow.length === 0) return true;
  if (!origin) return false;
  return allow.includes(origin.toLowerCase());
}

// ---------------------------------------------------------------------------
// ingest
// ---------------------------------------------------------------------------

async function ingest(request: Request): Promise<Response> {
  const origin = request.headers.get("origin");
  const cors = corsHeaders(origin);

  if (!originAllowed(origin, env.ALLOWED_ORIGINS)) {
    return new Response(null, { status: 403, headers: cors });
  }

  let raw: string;
  try {
    raw = await request.text();
  } catch {
    return new Response("unreadable body", { status: 400, headers: cors });
  }
  if (raw.length > MAX_BODY) {
    return new Response("body too large", { status: 413, headers: cors });
  }

  let body: Record<string, unknown>;
  try {
    body = JSON.parse(raw) as Record<string, unknown>;
  } catch {
    return new Response("invalid json", { status: 400, headers: cors });
  }

  const site = String(body.s ?? "").toLowerCase();
  if (!SITE_RE.test(site)) {
    return new Response("bad site", { status: 400, headers: cors });
  }

  const widthNum = Number(body.w);
  const width =
    Number.isFinite(widthNum) && widthNum > 0 && widthNum <= 10000 ? Math.round(widthNum) : null;

  const langRaw = String(body.l ?? "");
  const lang = LANG_RE.test(langRaw) ? langRaw.slice(0, 16) : null;

  const ts = Math.floor(Date.now() / 1000);
  const ip = request.headers.get("cf-connecting-ip") ?? "";
  const ua = request.headers.get("user-agent") ?? "";
  const { device, browser, os, bot } = parseUA(ua);

  const visitor = await mac(env.OSHIETE_SECRET, "oshiete/1/visitor", [ipPrefix(ip), ua, site], 8);
  const session = await mac(
    env.OSHIETE_SECRET,
    "oshiete/1/session",
    [visitor, String(Math.floor(ts / SESSION_BUCKET))],
    8,
  );

  const path = normalisePath(String(body.p ?? "/"));
  const ref = normaliseRef(String(body.r ?? ""));
  const country = (request.cf?.country as string | undefined) ?? null;

  await env.DB.batch([
    env.DB.prepare(
      `INSERT INTO events
         (ts, site, path, ref, country, device, browser, os, width, lang, visitor, session, is_bot)
       VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
    ).bind(ts, site, path, ref, country, device, browser, os, width, lang, visitor, session, bot ? 1 : 0),

    env.DB.prepare(
      `INSERT INTO visitors
         (visitor, site, first_seen, last_seen, sessions, last_session, country, device)
       VALUES (?, ?, ?, ?, 1, ?, ?, ?)
       ON CONFLICT(visitor) DO UPDATE SET
         last_seen    = MAX(visitors.last_seen, excluded.last_seen),
         sessions     = visitors.sessions + (visitors.last_session IS NOT excluded.last_session),
         last_session = excluded.last_session,
         country      = excluded.country,
         device       = excluded.device`,
    ).bind(visitor, site, ts, ts, session, country, device),
  ]);

  return new Response(null, { status: 204, headers: cors });
}

// ---------------------------------------------------------------------------
// sync
// ---------------------------------------------------------------------------

function authorised(request: Request): boolean {
  const auth = request.headers.get("authorization") ?? "";
  const token = auth.startsWith("Bearer ") ? auth.slice(7) : "";
  return Boolean(token) && Boolean(env.SYNC_TOKEN) && timingSafeEqual(token, env.SYNC_TOKEN);
}

async function since(request: Request): Promise<Response> {
  if (!authorised(request)) return new Response("unauthorized", { status: 401 });

  const url = new URL(request.url);
  const site = (url.searchParams.get("site") ?? "").toLowerCase();
  if (!SITE_RE.test(site)) return new Response("bad site", { status: 400 });

  const cursor = Number(url.searchParams.get("id") ?? "0");
  if (!Number.isInteger(cursor) || cursor < 0) return new Response("bad id", { status: 400 });

  const limitRaw = Number(url.searchParams.get("limit") ?? DEFAULT_LIMIT);
  const limit = Number.isInteger(limitRaw) && limitRaw > 0 ? Math.min(limitRaw, MAX_LIMIT) : DEFAULT_LIMIT;

  // One extra row tells us whether another page exists without a second query.
  const { results } = await env.DB.prepare(
    `SELECT id, ts, site, path, ref, country, device, browser, os, width, lang, visitor, session, is_bot
       FROM events
      WHERE site = ? AND id > ?
      ORDER BY id
      LIMIT ?`,
  )
    .bind(site, cursor, limit + 1)
    .all();

  const rows = results ?? [];
  const hasMore = rows.length > limit;
  if (hasMore) rows.length = limit;

  return Response.json(
    {
      events: rows,
      cursor: rows.length ? (rows[rows.length - 1] as { id: number }).id : cursor,
      has_more: hasMore,
    },
    { headers: { "cache-control": "no-store" } },
  );
}

/**
 * Site discovery for the mirror. `cursor` is the high-water mark to ask `/since`
 * for on a cold start, so the TUI never has to remember site names itself.
 */
async function sites(request: Request): Promise<Response> {
  if (!authorised(request)) return new Response("unauthorized", { status: 401 });

  const { results } = await env.DB.prepare(
    `SELECT site,
            COUNT(*) AS events,
            MAX(id)  AS cursor,
            MAX(ts)  AS last_seen
       FROM events
      GROUP BY site
      ORDER BY last_seen DESC`,
  ).all();

  return Response.json({ sites: results ?? [] }, { headers: { "cache-control": "no-store" } });
}

// ---------------------------------------------------------------------------

export default {
  async fetch(request: Request): Promise<Response> {
    const { pathname } = new URL(request.url);

    if (pathname === "/e") {
      if (request.method === "OPTIONS") {
        const h = corsHeaders(request.headers.get("origin"));
        h.set("access-control-allow-methods", "POST, OPTIONS");
        h.set("access-control-allow-headers", "content-type");
        h.set("access-control-max-age", "86400");
        return new Response(null, { status: 204, headers: h });
      }
      if (request.method !== "POST") return new Response("method not allowed", { status: 405 });
      return ingest(request);
    }

    if (pathname === "/b.js" && request.method === "GET") {
      return new Response(beaconSource, {
        headers: {
          "content-type": "text/javascript;charset=UTF-8",
          // Long enough to be kind to the CDN, short enough that a beacon fix
          // reaches every site within the hour.
          "cache-control": "public, max-age=3600",
          "access-control-allow-origin": "*",
        },
      });
    }

    if (pathname === "/since" && request.method === "GET") return since(request);
    if (pathname === "/sites" && request.method === "GET") return sites(request);

    if (pathname === "/health") {
      return Response.json({ ok: true, service: "oshiete", v: V });
    }

    return new Response("not found", { status: 404 });
  },
} satisfies ExportedHandler;
