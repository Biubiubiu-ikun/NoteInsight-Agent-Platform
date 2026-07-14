import http from 'k6/http';
import { check } from 'k6';
import exec from 'k6/execution';
import { Counter, Rate, Trend } from 'k6/metrics';

const PROFILE = (__ENV.PROFILE || 'baseline').toLowerCase();
const WORKLOAD = (__ENV.WORKLOAD || 'mixed').toLowerCase();
const ACCESS_PATTERN = (__ENV.ACCESS_PATTERN || 'uniform').toLowerCase();
const BASE_URL = __ENV.BASE_URL || 'http://host.docker.internal:18080';
const TOKEN_FILE = __ENV.TOKEN_FILE || '/work/backend-go/tmp/dev_tokens.csv';
const NOTE_START = positiveInt(__ENV.NOTE_START, 1);
const NOTE_COUNT = positiveInt(__ENV.NOTE_COUNT, 5000);
const COMMENT_START = positiveInt(__ENV.COMMENT_START, 1);
const COMMENT_COUNT = positiveInt(__ENV.COMMENT_COUNT, 20000);
const HOT_NOTE_COUNT = Math.min(positiveInt(__ENV.HOT_NOTE_COUNT, 100), NOTE_COUNT);

const endpointDurations = {
  notes_list: new Trend('notes_list_duration_ms', true),
  note_detail: new Trend('note_detail_duration_ms', true),
  comments_read: new Trend('comments_read_duration_ms', true),
  rankings_read: new Trend('rankings_read_duration_ms', true),
  note_like: new Trend('note_like_duration_ms', true),
  note_collect: new Trend('note_collect_duration_ms', true),
  note_share: new Trend('note_share_duration_ms', true),
  comment_create: new Trend('comment_create_duration_ms', true),
  comment_like: new Trend('comment_like_duration_ms', true),
};

const operationFailed = new Rate('operation_failed');
const writesAccepted = new Counter('writes_accepted_total');
const rateLimited = new Counter('rate_limited_total');
const loadBandDurations = {
  baseline: new Trend('load_baseline_duration_ms', true),
  low: new Trend('load_low_duration_ms', true),
  mid: new Trend('load_mid_duration_ms', true),
  high: new Trend('load_high_duration_ms', true),
  spike: new Trend('load_spike_duration_ms', true),
  recovery: new Trend('load_recovery_duration_ms', true),
};
const tokens = loadTokens();
const categories = ['beauty', 'skincare', 'fashion', 'food', 'travel', 'fitness', 'home', 'digital', 'parenting', 'workplace'];

export const options = {
  scenarios: buildScenarios(PROFILE),
  summaryTrendStats: ['min', 'avg', 'med', 'p(90)', 'p(95)', 'p(99)', 'max'],
  thresholds: {
    checks: [`rate>${__ENV.MIN_CHECK_RATE || '0.99'}`],
    operation_failed: [`rate<${__ENV.MAX_ERROR_RATE || '0.01'}`],
    http_req_failed: [`rate<${__ENV.MAX_ERROR_RATE || '0.01'}`],
    dropped_iterations: ['count<1'],
    'http_req_duration{kind:read}': [`p(95)<${__ENV.READ_P95_MS || '800'}`],
    'http_req_duration{kind:write}': [`p(95)<${__ENV.WRITE_P95_MS || '1000'}`],
    notes_list_duration_ms: [`p(95)<${__ENV.NOTES_LIST_P95_MS || '250'}`],
    note_detail_duration_ms: [`p(95)<${__ENV.NOTE_DETAIL_P95_MS || '150'}`],
    comments_read_duration_ms: [`p(95)<${__ENV.COMMENTS_READ_P95_MS || '100'}`],
    rankings_read_duration_ms: [`p(95)<${__ENV.RANKINGS_READ_P95_MS || '80'}`],
    note_like_duration_ms: [`p(95)<${__ENV.NOTE_LIKE_P95_MS || '150'}`],
    note_collect_duration_ms: [`p(95)<${__ENV.NOTE_COLLECT_P95_MS || '200'}`],
    note_share_duration_ms: [`p(95)<${__ENV.NOTE_SHARE_P95_MS || '200'}`],
    comment_create_duration_ms: [`p(95)<${__ENV.COMMENT_CREATE_P95_MS || '200'}`],
    comment_like_duration_ms: [`p(95)<${__ENV.COMMENT_LIKE_P95_MS || '150'}`],
  },
};

export function setup() {
  if (ACCESS_PATTERN !== 'uniform' && ACCESS_PATTERN !== 'hotspot') {
    throw new Error(`unsupported ACCESS_PATTERN ${ACCESS_PATTERN}`);
  }
  const ready = http.get(`${BASE_URL}/ready`, { tags: { endpoint: 'readiness', kind: 'control' } });
  if (ready.status !== 200) {
    throw new Error(`backend readiness failed: ${ready.status} ${ready.body}`);
  }
  if (workloadNeedsAuth(WORKLOAD) && tokens.length === 0) {
    throw new Error(`no development tokens loaded from ${TOKEN_FILE}`);
  }
  return { started_at: new Date().toISOString() };
}

export default function () {
  const noteID = pickNoteID();
  const commentID = COMMENT_START + ((__VU * 1499 + __ITER * 31) % COMMENT_COUNT);
  const token = tokens.length > 0 ? tokens[(__VU * 7919 + __ITER) % tokens.length] : '';

  if (WORKLOAD !== 'mixed') {
    runNamedWorkload(WORKLOAD, noteID, commentID, token);
    return;
  }

  // A current-domain adaptation of the first plan: 85% public reads and 15% authenticated writes.
  const bucket = (__VU * 37 + __ITER * 19) % 100;
  if (bucket < 20) {
    listNotes(noteID);
  } else if (bucket < 45) {
    getNote(noteID);
  } else if (bucket < 70) {
    listComments(noteID);
  } else if (bucket < 85) {
    listRankings();
  } else if (bucket < 90) {
    likeNote(noteID, token);
  } else if (bucket < 93) {
    collectNote(noteID, token);
  } else if (bucket < 95) {
    shareNote(noteID, token);
  } else if (bucket < 98) {
    createComment(noteID, token);
  } else {
    likeComment(commentID, token);
  }
}

export function handleSummary(data) {
  const markdown = renderMarkdownSummary(data);
  return {
    '/results/summary.json': JSON.stringify(data, null, 2),
    '/results/summary.md': markdown,
    stdout: `${markdown}\n`,
  };
}

function buildScenarios(profile) {
  const preAllocatedVUs = positiveInt(__ENV.PREALLOCATED_VUS, 40);
  const maxVUs = positiveInt(__ENV.MAX_VUS, 200);
  const common = {
    exec: 'default',
    timeUnit: '1s',
    preAllocatedVUs,
    maxVUs,
    gracefulStop: __ENV.GRACEFUL_STOP || '15s',
    tags: { phase: 'phase6', profile, workload: WORKLOAD, access_pattern: ACCESS_PATTERN },
  };

  switch (profile) {
    case 'baseline':
      return {
        capacity: {
          ...common,
          executor: 'constant-arrival-rate',
          rate: positiveInt(__ENV.RATE, 25),
          duration: __ENV.DURATION || '45s',
        },
      };
    case 'step':
      return {
        capacity: {
          ...common,
          executor: 'ramping-arrival-rate',
          startRate: positiveInt(__ENV.START_RPS, 10),
          stages: [
            { target: positiveInt(__ENV.STEP_LOW_RPS, 25), duration: __ENV.RAMP_DURATION || '10s' },
            { target: positiveInt(__ENV.STEP_LOW_RPS, 25), duration: __ENV.STEP_DURATION || '20s' },
            { target: positiveInt(__ENV.STEP_MID_RPS, 50), duration: __ENV.RAMP_DURATION || '10s' },
            { target: positiveInt(__ENV.STEP_MID_RPS, 50), duration: __ENV.STEP_DURATION || '20s' },
            { target: positiveInt(__ENV.STEP_HIGH_RPS, 75), duration: __ENV.RAMP_DURATION || '10s' },
            { target: positiveInt(__ENV.STEP_HIGH_RPS, 75), duration: __ENV.STEP_DURATION || '20s' },
            { target: 0, duration: __ENV.RAMP_DURATION || '10s' },
          ],
        },
      };
    case 'spike':
      return {
        capacity: {
          ...common,
          executor: 'ramping-arrival-rate',
          startRate: positiveInt(__ENV.START_RPS, 10),
          stages: [
            { target: positiveInt(__ENV.SPIKE_BASE_RPS, 20), duration: '10s' },
            { target: positiveInt(__ENV.SPIKE_BASE_RPS, 20), duration: '10s' },
            { target: positiveInt(__ENV.SPIKE_RPS, 120), duration: '5s' },
            { target: positiveInt(__ENV.SPIKE_RPS, 120), duration: __ENV.SPIKE_DURATION || '15s' },
            { target: positiveInt(__ENV.SPIKE_BASE_RPS, 20), duration: '5s' },
            { target: positiveInt(__ENV.SPIKE_BASE_RPS, 20), duration: '10s' },
            { target: 0, duration: '5s' },
          ],
        },
      };
    case 'soak':
      return {
        capacity: {
          ...common,
          executor: 'constant-arrival-rate',
          rate: positiveInt(__ENV.RATE, 30),
          duration: __ENV.DURATION || '10m',
        },
      };
    default:
      throw new Error(`unsupported PROFILE ${profile}; use baseline, step, spike, or soak`);
  }
}

function runNamedWorkload(workload, noteID, commentID, token) {
  switch (workload) {
    case 'notes_list':
      listNotes(noteID);
      return;
    case 'note_detail':
      getNote(noteID);
      return;
    case 'comments_read':
      listComments(noteID);
      return;
    case 'rankings_read':
      listRankings();
      return;
    case 'writes':
      runWriteMix(noteID, commentID, token);
      return;
    default:
      throw new Error(`unsupported WORKLOAD ${workload}`);
  }
}

function runWriteMix(noteID, commentID, token) {
  const bucket = (__VU + __ITER) % 5;
  if (bucket === 0) likeNote(noteID, token);
  else if (bucket === 1) collectNote(noteID, token);
  else if (bucket === 2) shareNote(noteID, token);
  else if (bucket === 3) createComment(noteID, token);
  else likeComment(commentID, token);
}

function listNotes(noteID) {
  const category = categories[noteID % categories.length];
  request('notes_list', 'GET', `${BASE_URL}/api/v1/notes?category=${category}&limit=20`, null, publicParams('notes_list'), [200]);
}

function getNote(noteID) {
  request('note_detail', 'GET', `${BASE_URL}/api/v1/notes/${noteID}`, null, publicParams('note_detail'), [200]);
}

function listComments(noteID) {
  request('comments_read', 'GET', `${BASE_URL}/api/v1/notes/${noteID}/comments?limit=20`, null, publicParams('comments_read'), [200]);
}

function listRankings() {
  request('rankings_read', 'GET', `${BASE_URL}/api/v1/rankings/notes/daily?limit=20`, null, publicParams('rankings_read'), [200]);
}

function likeNote(noteID, token) {
  request('note_like', 'POST', `${BASE_URL}/api/v1/notes/${noteID}/like`, '{}', authParams(token, 'note_like'), [200]);
}

function collectNote(noteID, token) {
  request('note_collect', 'POST', `${BASE_URL}/api/v1/notes/${noteID}/collect`, JSON.stringify({ collection_name: 'phase6-capacity' }), authParams(token, 'note_collect'), [200]);
}

function shareNote(noteID, token) {
  request('note_share', 'POST', `${BASE_URL}/api/v1/notes/${noteID}/share`, JSON.stringify({ channel: 'phase6_capacity' }), authParams(token, 'note_share'), [200]);
}

function createComment(noteID, token) {
  const content = `Phase 6 capacity feedback ${__VU}-${__ITER}: the steps and constraints are clear enough to compare with my own use case.`;
  request('comment_create', 'POST', `${BASE_URL}/api/v1/notes/${noteID}/comments`, JSON.stringify({ content, intent: 'load_test' }), authParams(token, 'comment_create'), [201]);
}

function likeComment(commentID, token) {
  request('comment_like', 'POST', `${BASE_URL}/api/v1/comments/${commentID}/like`, '{}', authParams(token, 'comment_like'), [200]);
}

function request(endpoint, method, url, body, params, expectedStatuses) {
  const loadBand = currentLoadBand();
  params.tags.load_band = loadBand;
  const response = http.request(method, url, body, params);
  endpointDurations[endpoint].add(response.timings.duration, { load_band: loadBand });
  loadBandDurations[loadBand].add(response.timings.duration, { kind: params.tags.kind });
  const ok = check(response, {
    [`${endpoint} returned expected status`]: (res) => expectedStatuses.includes(res.status),
  });
  operationFailed.add(!ok, { endpoint, load_band: loadBand });
  if (response.status === 429) {
    rateLimited.add(1, { endpoint });
  }
  if (params.tags.kind === 'write' && ok) {
    writesAccepted.add(1, { endpoint });
  }
  return response;
}

function publicParams(endpoint) {
  return { tags: { phase: 'phase6', profile: PROFILE, workload: WORKLOAD, kind: 'read', endpoint } };
}

function authParams(token, endpoint) {
  return {
    headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' },
    tags: { phase: 'phase6', profile: PROFILE, workload: WORKLOAD, kind: 'write', endpoint },
  };
}

function workloadNeedsAuth(workload) {
  return workload === 'mixed' || workload === 'writes';
}

function loadTokens() {
  try {
    return open(TOKEN_FILE)
      .trim()
      .split(/\r?\n/)
      .slice(1)
      .map((line) => line.split(',')[1])
      .filter(Boolean);
  } catch (_) {
    return [];
  }
}

function positiveInt(value, fallback) {
  const parsed = Number(value || fallback);
  if (!Number.isInteger(parsed) || parsed <= 0) {
    throw new Error(`expected a positive integer, got ${value}`);
  }
  return parsed;
}

function currentLoadBand() {
  if (PROFILE === 'baseline' || PROFILE === 'soak') return 'baseline';

  const elapsed = exec.instance.currentTestRunDuration;
  if (PROFILE === 'step') {
    const ramp = parseDuration(__ENV.RAMP_DURATION || '10s');
    const hold = parseDuration(__ENV.STEP_DURATION || '20s');
    if (elapsed < ramp + hold) return 'low';
    if (elapsed < 2 * (ramp + hold)) return 'mid';
    if (elapsed < 3 * (ramp + hold)) return 'high';
    return 'recovery';
  }
  if (PROFILE === 'spike') {
    if (elapsed < 20000) return 'low';
    if (elapsed < 40000) return 'spike';
    return 'recovery';
  }
  return 'baseline';
}

function parseDuration(value) {
  const match = /^(\d+)(ms|s|m)$/.exec(value);
  if (!match) throw new Error(`unsupported duration ${value}`);
  const amount = Number(match[1]);
  if (match[2] === 'ms') return amount;
  if (match[2] === 's') return amount * 1000;
  return amount * 60000;
}

function renderMarkdownSummary(data) {
  const lines = [
    '# Phase 6 k6 Summary',
    '',
    `- Profile: ${PROFILE}`,
    `- Workload: ${WORKLOAD}`,
    `- Access pattern: ${ACCESS_PATTERN}`,
    `- Base URL: ${BASE_URL}`,
    `- Notes sampled: ${NOTE_START}..${NOTE_START + NOTE_COUNT - 1}`,
    `- Comments sampled: ${COMMENT_START}..${COMMENT_START + COMMENT_COUNT - 1}`,
    '',
    '| Metric | Count/Rate | P50 | P95 | P99 |',
    '| --- | ---: | ---: | ---: | ---: |',
  ];

  lines.push(metricRow(data, 'http_reqs'));
  lines.push(metricRow(data, 'http_req_failed'));
  lines.push(metricRow(data, 'http_req_duration'));
  for (const metric of Object.keys(endpointDurations)) {
    lines.push(metricRow(data, `${metric}_duration_ms`));
  }
  for (const band of Object.keys(loadBandDurations)) {
    lines.push(metricRow(data, `load_${band}_duration_ms`));
  }
  lines.push(metricRow(data, 'writes_accepted_total'));
  lines.push(metricRow(data, 'rate_limited_total'));
  lines.push('', `Threshold result: ${thresholdsPassed(data) ? 'PASS' : 'FAIL'}`);
  return lines.join('\n');
}

function pickNoteID() {
  const selector = (__VU * 997 + __ITER * 17) >>> 0;
  if (ACCESS_PATTERN === 'hotspot' && selector % 100 < 80) {
    const hotSelector = (__VU * 577 + __ITER * 7919) >>> 0;
    return NOTE_START + (hotSelector % HOT_NOTE_COUNT);
  }
  return NOTE_START + (selector % NOTE_COUNT);
}

function metricRow(data, name) {
  const metric = data.metrics[name];
  if (!metric) return `| ${name} | n/a | n/a | n/a | n/a |`;
  const values = metric.values || {};
  const countOrRate = values.count !== undefined && values.rate !== undefined
    ? `${format(values.count)} (${format(values.rate)}/s)`
    : format(values.count !== undefined ? values.count : values.rate);
  return `| ${name} | ${countOrRate} | ${format(values.med)} | ${format(values['p(95)'])} | ${format(values['p(99)'])} |`;
}

function format(value) {
  if (value === undefined || value === null) return 'n/a';
  return Number(value).toFixed(2);
}

function thresholdsPassed(data) {
  for (const metric of Object.values(data.metrics)) {
    if (!metric.thresholds) continue;
    for (const threshold of Object.values(metric.thresholds)) {
      if (!threshold.ok) return false;
    }
  }
  return true;
}
