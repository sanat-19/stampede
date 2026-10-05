// Concert-sale spike against POST /book, open model (ramping-arrival-rate):
// requests arrive on schedule whether or not the server has answered.
//
//   0 → PEAK_RPS in 10 s, hold 30 s, decay to 10% over 2 min, hold 10% to DURATION_MIN.
//
// The test stops early once the event is sold out (STOP_ON_SOLD_OUT=false to run the full duration).
import http from 'k6/http';
import exec from 'k6/execution';
import { Counter, Trend } from 'k6/metrics';
import { textSummary } from 'https://jslib.k6.io/k6-summary/0.0.2/index.js';

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';
const PEAK_RPS = parseInt(__ENV.PEAK_RPS || '5000', 10);
const USERS = parseInt(__ENV.USERS || '200000', 10);
const DURATION_MIN = parseFloat(__ENV.DURATION_MIN || '10');
const EVENT_ID = parseInt(__ENV.EVENT_ID || '1', 10);
// Each in-flight request holds a VU; at 5 s client timeout the worst case is PEAK_RPS × 5.
const PRE_VUS = parseInt(__ENV.PRE_VUS || String(PEAK_RPS), 10);
const MAX_VUS = parseInt(__ENV.MAX_VUS || String(PEAK_RPS * 5), 10);
const SUMMARY_FILE = __ENV.SUMMARY_FILE || '';
const STOP_ON_SOLD_OUT = (__ENV.STOP_ON_SOLD_OUT || 'true') !== 'false';

const LOW_RPS = Math.max(1, Math.round(PEAK_RPS * 0.1));
const TAIL_S = Math.max(0, Math.round(DURATION_MIN * 60) - 160);

const stages = [
  { target: PEAK_RPS, duration: '10s' },
  { target: PEAK_RPS, duration: '30s' },
  { target: LOW_RPS, duration: '2m' },
];
if (TAIL_S > 0) stages.push({ target: LOW_RPS, duration: `${TAIL_S}s` });

// client_error = no HTTP response (k6 timeout, connection refused/reset).
const OUTCOMES = ['booked', 'already_booked', 'sold_out', 'invalid', 'timeout_pool', 'timeout_db', 'error', 'client_error'];
// 409 sold_out is an expected answer, not a failed request.
http.setResponseCallback(http.expectedStatuses(200, 201, 409));

const counters =Object.fromEntries(OUTCOMES.map((o) => [o, new Counter(`outcome_${o}`)]));
const latency = new Trend('book_latency', true);
// From the server's Server-Timing header: where each request's time went.
const poolWait = new Trend('server_pool_wait', true); // waiting for a DB connection
const dbTime = new Trend('server_db_time', true); // inside the transaction

export const options = {
  scenarios: {
    spike: {
      executor: 'ramping-arrival-rate',
      startRate: 0,
      timeUnit: '1s',
      preAllocatedVUs: PRE_VUS,
      maxVUs: MAX_VUS,
      stages,
    },
  },
  // Trivial thresholds so the per-outcome latency submetrics appear in the summary.
  thresholds: Object.fromEntries(
    ['book_latency', 'server_pool_wait', 'server_db_time'].flatMap((m) =>
      OUTCOMES.map((o) => [`${m}{outcome:${o}}`, ['max>=0']]),
    ),
  ),
  summaryTrendStats: ['avg', 'min', 'med', 'p(90)', 'p(95)', 'p(99)', 'max'],
};

export default function () {
  const userId = Math.floor(Math.random() * USERS) + 1; // repeats → already_booked, mimics retries
  const qty = Math.floor(Math.random() * 4) + 1;

  const res = http.post(
    `${BASE_URL}/book`,
    JSON.stringify({ event_id: EVENT_ID, user_id: userId, qty }),
    { headers: { 'Content-Type': 'application/json' }, timeout: '5s', tags: { name: 'book' } },
  );

  let outcome = 'client_error';
  if (res.status !== 0) {
    try {
      outcome = res.json('outcome');
    } catch (e) {
      outcome = 'error';
    }
    if (!counters[outcome]) outcome = 'error';
  }

  counters[outcome].add(1);
  latency.add(res.timings.duration, { outcome });
  recordServerTiming(res.headers['Server-Timing'], outcome);

  // The server sets event_sold_out only once no ticket is left (a sold_out for
  // asking 4 with 1–3 left does not have it), so one such answer ends the test.
  if (outcome === 'sold_out' && STOP_ON_SOLD_OUT && res.json('event_sold_out') === true) {
    exec.test.abort('all tickets sold out');
  }
}

// Parses "pool;dur=12.34, db;dur=5.67, total;dur=18.01" into the two trends.
function recordServerTiming(header, outcome) {
  if (!header) return;
  for (const part of header.split(',')) {
    const [name, dur] = part.trim().split(';dur=');
    const value = parseFloat(dur);
    if (Number.isNaN(value)) continue;
    if (name === 'pool') poolWait.add(value, { outcome });
    if (name === 'db') dbTime.add(value, { outcome });
  }
}

export function handleSummary(data) {
  const value = (name, field) => (data.metrics[name] ? data.metrics[name].values[field] : 0);

  const total = OUTCOMES.reduce((sum, o) => sum + value(`outcome_${o}`, 'count'), 0);
  const lines = ['', '█ OUTCOMES', ''];
  for (const o of OUTCOMES) {
    const n = value(`outcome_${o}`, 'count');
    const pct = total ? ((n / total) * 100).toFixed(1) : '0.0';
    lines.push(`    ${o.padEnd(16)} ${String(n).padStart(9)}  ${pct.padStart(5)}%`);
  }
  lines.push(`    ${'total'.padEnd(16)} ${String(total).padStart(9)}`);

  const dropped = value('dropped_iterations', 'count');
  lines.push('', `    dropped_iterations ${dropped}`);
  if (dropped > 0) {
    lines.push('    WARNING: k6 could not keep up with the arrival rate — the load generator, not the server, was the bottleneck.');
  }
  lines.push('');

  const out = { stdout: textSummary(data, { indent: ' ', enableColors: true }) + lines.join('\n') + '\n' };
  if (SUMMARY_FILE) out[SUMMARY_FILE] = JSON.stringify(data, null, 2);
  return out;
}
