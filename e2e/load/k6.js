// k6 load profile for jackui — moderate profile for a 2-OCPU aarch64 node.
// Safe reads of the API surface plus net-zero writes and a login beat; envs:
//   TARGET (base URL), ADMIN_USER/ADMIN_PASS (sweep admin), SMOKE=1 (20s run).
// Full profile: 2m ramp to 10 VUs → 2m plateau → 30s spike to 25 → ramp down,
// with constant-arrival writes and logins running concurrently.
// Run (host network):
//   docker run --rm -i -e TARGET=http://HOST:PORT -e ADMIN_PASS='…' \
//     grafana/k6 run - < e2e/load/k6.js
// Run (inside the stack's compose network):
//   docker run --rm --network <stack_network> -v "$PWD":/work -w /work \
//     -e TARGET=http://<app>:8989 -e ADMIN_PASS='…' grafana/k6 run /work/e2e/load/k6.js
//
// Design notes:
// - Auth is enabled on the target; setup() logs in once with the sweep admin
//   (seeded via JACKUI_ADMIN_PASSWORD; username defaults to "admin", see
//   cmd/server/init_auth.go:99-101) and every VU sends the access JWT via
//   Authorization: Bearer (internal/auth/middleware.go:205-209).
// - Reads: the safe GET subset of the inventory (no SSE, no /debug/pprof, no
//   media/HLS streams, no external-AI endpoints that hang without providers).
// - Writes: playlist create+delete (net zero) and notifications/read at low
//   arrival rates so the run does not bury the DB in junk rows.
// - Logins use CORRECT credentials only (wrong ones trip handlers/auth.go:56
//   lockout.Fail and would 429-poison the results).
// - Per-route p95 comes from one Trend per route; per-route errors from one
//   Counter per route (status != expected).

import http from 'k6/http';
import { check, sleep, fail } from 'k6';
import { Trend, Counter } from 'k6/metrics';

const BASE = (__ENV.TARGET || 'http://127.0.0.1:30108').replace(/\/$/, '');
const ADMIN_USER = __ENV.ADMIN_USER || 'admin';
const ADMIN_PASS = __ENV.ADMIN_PASS || 'SweepJackui-2026-Admin!';

// expected: HTTP status the route returns on a healthy app (probed before the run).
// tag: short metric prefix.
const PAGE_ROUTES = [
  { tag: 'page_root',      path: '/' },
  { tag: 'page_login',     path: '/login' },
  { tag: 'page_library',   path: '/library' },
  { tag: 'page_discover',  path: '/discover' },
  { tag: 'page_stats',     path: '/stats' },
  { tag: 'page_settings',  path: '/settings' },
];

const PUB_GET_ROUTES = [
  { tag: 'pub_healthz',   path: '/healthz',            expected: 200 },
  { tag: 'pub_status',    path: '/status',             expected: 200 },
  { tag: 'pub_auth_conf', path: '/api/auth/config',    expected: 200 },
];

// Authed GET reads (Bearer). expected = probed status on the healthy app.
// /api/status probed 503: its handler checks Jackett, which is deliberately
// unreachable on the sweep stack (JACKETT_URL=http://127.0.0.1:9).
const AUTHED_GET_ROUTES = [
  { tag: 'api_status',            path: '/api/status',  expected: 503 },
  { tag: 'api_history',           path: '/api/history?page=1', expected: 200 },
  { tag: 'api_history_cache',     path: '/api/history/cache?q=k6', expected: 200 },
  { tag: 'api_library',           path: '/api/library', expected: 200 },
  { tag: 'api_playlists',         path: '/api/playlists', expected: 200 },
  { tag: 'api_watchlists',        path: '/api/watchlists', expected: 200 },
  { tag: 'api_downloads',         path: '/api/downloads', expected: 200 },
  { tag: 'api_downloads_cat',     path: '/api/downloads/categories', expected: 200 },
  { tag: 'api_stream_favorites',  path: '/api/stream/favorites', expected: 200 },
  { tag: 'api_stream_active',     path: '/api/stream/active', expected: 200 },
  { tag: 'api_stream_limits',     path: '/api/stream/limits', expected: 200 },
  { tag: 'api_transcode_active',  path: '/api/transcode/active', expected: 200 },
  { tag: 'api_transcode_cap',     path: '/api/transcode/capabilities', expected: 200 },
  { tag: 'api_notifications',     path: '/api/notifications', expected: 200 },
  { tag: 'api_set_subtitles',     path: '/api/settings/subtitles', expected: 200 },
  { tag: 'api_stream_settings',   path: '/api/stream/settings', expected: 200 },
  { tag: 'api_downloads_settings',path: '/api/downloads/settings', expected: 200 },
  { tag: 'api_config',            path: '/api/config', expected: 200 },
  { tag: 'api_stats',             path: '/api/stats', expected: 200 },
  { tag: 'api_local_mounts',      path: '/api/local/mounts', expected: 200 },
  { tag: 'api_mounts',            path: '/api/mounts', expected: 200 },
  { tag: 'api_subtitles_enabled', path: '/api/subtitles/enabled', expected: 200 },
  { tag: 'api_recommendations',   path: '/api/recommendations', expected: 200 },
  { tag: 'api_transfers',         path: '/api/transfers', expected: 200 },
  { tag: 'api_push_vapid',        path: '/api/push/vapid', expected: 200 },
  { tag: 'api_auth_me',           path: '/api/auth/me', expected: 200 },
  { tag: 'api_auth_providers',    path: '/api/auth/oauth/providers', expected: 200 },
];

// Reads that depend on external providers (Jackett/TMDB are NOT configured on
// the sweep stack). Probed statuses recorded above the run; a different status
// counts as an error.
const EXT_GET_ROUTES = [
  { tag: 'api_search',            path: '/api/search?q=k6-sweep', expected: 502 },
  { tag: 'api_indexers',          path: '/api/indexers', expected: 502 },
  { tag: 'api_tmdb_trending',     path: '/api/tmdb/trending', expected: 503 },
];

// All metrics MUST be declared in the init context (top-level) — k6 forbids
// creating them inside scenario functions ("metrics must be declared in the
// init context"), so every route's Trend/Counter is created eagerly here.
const trends = {};
const errs = {};
const WRITE_TAGS = ['w_playlist_create', 'w_playlist_delete', 'w_notif_read'];
for (const tag of [
  ...PAGE_ROUTES.map((r) => r.tag),
  ...PUB_GET_ROUTES.map((r) => r.tag),
  ...AUTHED_GET_ROUTES.map((r) => r.tag),
  ...EXT_GET_ROUTES.map((r) => r.tag),
  ...WRITE_TAGS,
  'auth_login',
  'api_auth_sessions', // POST ping done in readScenario
]) {
  trends[tag] = new Trend(`t_${tag}_dur`, true);
  errs[tag] = new Counter(`c_${tag}_err`);
}
function metricsFor(tag) {
  return trends[tag];
}

function expectStatus(tag, res, expected) {
  metricsFor(tag).add(res.timings.duration);
  const ok = res.status === expected;
  if (!ok) errs[tag].add(1);
  check(res, { [`${tag} status ${expected}`]: () => ok }, { route: tag });
}

function authHeaders(token) {
  return { headers: { Authorization: `Bearer ${token}` } };
}

function loginOnce() {
  const res = http.post(`${BASE}/api/auth/login`,
    JSON.stringify({ username: ADMIN_USER, password: ADMIN_PASS, remember: false }),
    { headers: { 'Content-Type': 'application/json' }, tags: { route: 'auth_login' } });
  if (res.status !== 200) {
    fail(`setup login failed: status=${res.status} body=${res.body.slice(0, 200)}`);
  }
  const access = res.json('access');
  if (!access) fail('setup login returned no access token');
  return access;
}

export function setup() {
  return { token: loginOnce() };
}

const SMOKE = __ENV.SMOKE === '1';

export const options = {
  // NOTE: bodies must be kept — setup() reads the access token from the login
  // response and writeScenario reads the created playlist id.
  thresholds: {
    checks: ['rate>=0.90'],
    'http_req_duration{name:page}': ['p(95)<3000'],
    'http_req_duration{name:read}': ['p(95)<3000'],
  },
  scenarios: {
    reads: {
      executor: 'ramping-vus',
      startVUs: 0,
      stages: SMOKE
        ? [
            { duration: '5s', target: 2 },
            { duration: '5s', target: 2 },
            { duration: '5s', target: 3 },
            { duration: '5s', target: 0 },
          ]
        : [
            { duration: '2m', target: 10 },  // rampa
            { duration: '2m', target: 10 },  // patamar
            { duration: '30s', target: 25 }, // spike
            { duration: '30s', target: 0 },  // descida
          ],
      gracefulRampDown: '15s',
      exec: 'readScenario',
    },
    writes: {
      executor: 'constant-arrival-rate',
      rate: 1, timeUnit: '1s',
      duration: SMOKE ? '18s' : '4m30s',
      preAllocatedVUs: 4, maxVUs: 8,
      startTime: SMOKE ? '2s' : '1m',
      exec: 'writeScenario',
    },
    auth: {
      executor: 'constant-arrival-rate',
      rate: 1, timeUnit: '4s',
      duration: SMOKE ? '18s' : '4m30s',
      preAllocatedVUs: 2, maxVUs: 4,
      startTime: SMOKE ? '2s' : '1m',
      exec: 'authScenario',
    },
  },
  summaryTrendStats: ['avg', 'min', 'med', 'max', 'p(90)', 'p(95)'],
};

export function readScenario(data) {
  const token = data.token;
  const H = authHeaders(token);
  const page = PAGE_ROUTES[Math.floor(Math.random() * PAGE_ROUTES.length)];
  const pres = http.get(`${BASE}${page.path}`, { tags: { route: page.tag, name: 'page' } });
  expectStatus(page.tag, pres, page.expected || 200);

  for (const r of PUB_GET_ROUTES) {
    expectStatus(r.tag, http.get(`${BASE}${r.path}`, { tags: { route: r.tag, name: 'read' } }), r.expected);
  }
  for (const r of AUTHED_GET_ROUTES) {
    expectStatus(r.tag, http.get(`${BASE}${r.path}`, H && { ...H, tags: { route: r.tag, name: 'read' } }), r.expected);
  }
  // /api/auth/sessions is POST-only (cmd/server/routes.go:687).
  expectStatus('api_auth_sessions', http.post(`${BASE}/api/auth/sessions`, JSON.stringify({}), { ...H, 'Content-Type': 'application/json', tags: { route: 'api_auth_sessions', name: 'read' } }), 200);
  // External-provider reads: expected to return the probed provider-missing status.
  for (const r of EXT_GET_ROUTES) {
    expectStatus(r.tag, http.get(`${BASE}${r.path}`, H && { ...H, tags: { route: r.tag, name: 'ext' } }), r.expected);
  }
  sleep(0.3);
}

export function writeScenario(data) {
  const H = { ...authHeaders(data.token), 'Content-Type': 'application/json' };
  const name = `k6-load-${__VU}-${Date.now()}`;
  const create = http.post(`${BASE}/api/playlists`, JSON.stringify({ name }), H);
  expectStatus('w_playlist_create', create, 200); // handlers/playlists.go:44 c.JSON(http.StatusOK, p)
  const id = create.status === 200 ? create.json('id') : null; // internal/playlists/store.go:23 json:"id"
  if (id != null) {
    const del = http.del(`${BASE}/api/playlists/${id}`, null, authHeaders(data.token));
    expectStatus('w_playlist_delete', del, 200); // handlers/playlists.go:109
  }
  const read = http.post(`${BASE}/api/notifications/read`, JSON.stringify({}), H);
  expectStatus('w_notif_read', read, 200); // handlers/push.go:120 MarkAllRead, body ignored
  sleep(0.5);
}

export function authScenario() {
  // Correct credentials only (wrong ones poison the lockout counters).
  const res = http.post(`${BASE}/api/auth/login`,
    JSON.stringify({ username: ADMIN_USER, password: ADMIN_PASS, remember: false }),
    { headers: { 'Content-Type': 'application/json' }, tags: { route: 'auth_login', name: 'read' } });
  metricsFor('auth_login').add(res.timings.duration);
  const ok = res.status === 200;
  if (!ok) errs['auth_login'].add(1);
  check(res, { 'auth_login 200': () => ok }, { route: 'auth_login' });
}

export function handleSummary(data) {
  const lines = [];
  lines.push('==== PER-ROUTE p95 / ERR ====');
  const sorted = Object.keys(trends).sort();
  for (const tag of sorted) {
    const t = trends[tag];
    const dur = data.metrics[`t_${tag}_dur`];
    const err = data.metrics[`c_${tag}_err`];
    const p95 = dur ? dur.values['p(95)'] : 0;
    const cnt = err ? err.values.count : 0;
    lines.push(`${tag.padEnd(26)} p95=${p95.toFixed(1)}ms errors=${cnt}`);
  }
  const agg = data.metrics.http_req_duration.values;
  const failed = data.metrics.http_req_failed.values;
  lines.push('==== AGGREGATE ====');
  lines.push(`http_req_duration p95=${agg['p(95)'].toFixed(1)}ms avg=${agg.avg.toFixed(1)}ms`);
  lines.push(`http_req_failed rate=${(failed.rate * 100).toFixed(2)}%`);
  return { stdout: lines.join('\n') + '\n' };
}
