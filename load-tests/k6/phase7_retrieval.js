import http from 'k6/http';
import { check } from 'k6';
import exec from 'k6/execution';
import { Counter, Rate, Trend } from 'k6/metrics';

const PROFILE = (__ENV.PROFILE || 'baseline').toLowerCase();
const MODE = (__ENV.MODE || 'mixed').toLowerCase();
const BASE_URL = __ENV.BASE_URL || 'http://host.docker.internal:18080';
const BENCHMARK_FILE = __ENV.BENCHMARK_FILE || '/work/evaluation/benchmarks/retrieval_v4/development.jsonl';
const TOKEN_FILE = __ENV.TOKEN_FILE || '/work/backend-go/tmp/dev_tokens.csv';
const PROJECT_ID = positiveInt(__ENV.PROJECT_ID, 1);
const DATASET_VERSION_ID = positiveInt(__ENV.DATASET_VERSION_ID, 2);
const INGESTION_RUN_ID = __ENV.INGESTION_RUN_ID || 'phase7a_dv2_rebuild_v2_20260718';
const modes = MODE === 'mixed' ? ['lexical', 'vector', 'hybrid'] : [MODE];
const cases = loadCases();
const tokens = loadTokens();

const retrievalFailed = new Rate('retrieval_failed');
const citationInvalid = new Rate('citation_invalid');
const retrievalRateLimited = new Rate('retrieval_rate_limited');
const retrievalTimedOut = new Rate('retrieval_timed_out');
const noRelevantDecisions = new Counter('no_relevant_decisions_total');
const modeDurations = {
  lexical: new Trend('retrieval_lexical_duration_ms', true),
  vector: new Trend('retrieval_vector_duration_ms', true),
  hybrid: new Trend('retrieval_hybrid_duration_ms', true),
};

export const options = {
  scenarios: buildScenarios(PROFILE),
  summaryTrendStats: ['min', 'avg', 'med', 'p(90)', 'p(95)', 'p(99)', 'max'],
  thresholds: {
    checks: [`rate>${__ENV.MIN_CHECK_RATE || '0.98'}`],
    retrieval_failed: [`rate<${__ENV.MAX_ERROR_RATE || '0.02'}`],
    retrieval_rate_limited: [`rate<${__ENV.MAX_RATE_LIMIT_RATE || '0.001'}`],
    retrieval_timed_out: [`rate<${__ENV.MAX_TIMEOUT_RATE || __ENV.MAX_ERROR_RATE || '0.02'}`],
    citation_invalid: [`rate<${__ENV.MAX_CITATION_ERROR_RATE || '0.001'}`],
    http_req_failed: [`rate<${__ENV.MAX_ERROR_RATE || '0.02'}`],
    dropped_iterations: ['count<1'],
    retrieval_lexical_duration_ms: [`p(95)<${__ENV.LEXICAL_P95_MS || '4000'}`],
    retrieval_vector_duration_ms: [`p(95)<${__ENV.VECTOR_P95_MS || '2000'}`],
    retrieval_hybrid_duration_ms: [`p(95)<${__ENV.HYBRID_P95_MS || '4000'}`],
  },
};

export function setup() {
  for (const mode of modes) {
    if (!['lexical', 'vector', 'hybrid'].includes(mode)) {
      throw new Error(`unsupported MODE ${MODE}`);
    }
  }
  if (cases.length === 0) {
    throw new Error(`no public development cases loaded from ${BENCHMARK_FILE}`);
  }
  const ready = http.get(`${BASE_URL}/ready`, { tags: { endpoint: 'readiness', mode: 'control' } });
  if (ready.status !== 200) {
    throw new Error(`backend readiness failed: ${ready.status} ${ready.body}`);
  }
  return { started_at: new Date().toISOString(), case_count: cases.length };
}

export default function () {
  const mode = modes[(__VU * 17 + __ITER) % modes.length];
  const evalCase = cases[(__VU * 7919 + __ITER * 31) % cases.length];
  const token = tokens.length > 0 ? tokens[(__VU * 577 + __ITER) % tokens.length] : '';
  const response = http.post(
    `${BASE_URL}/api/v1/retrieval/search`,
    JSON.stringify({
      project_id: PROJECT_ID,
      dataset_version_id: DATASET_VERSION_ID,
      ingestion_run_id: INGESTION_RUN_ID,
      query: evalCase.query,
      mode,
      limit: 10,
    }),
    requestParams(mode, token),
  );

  modeDurations[mode].add(response.timings.duration, { task_type: evalCase.task_type });
  let payload = null;
  try {
    payload = response.json();
  } catch (_) {
    payload = null;
  }
  const citationsValid = payload === null || !Array.isArray(payload.results)
    ? false
    : payload.results.every((result) => Array.isArray(result.citations) && result.citations.length > 0);
  const ok = check(response, {
    'retrieval returned 200': (res) => res.status === 200,
    'retrieval mode matches': () => payload !== null && payload.mode === mode,
    'retrieval snapshot matches': () => payload !== null && payload.scope &&
      payload.scope.dataset_version_id === DATASET_VERSION_ID && payload.scope.ingestion_run_id === INGESTION_RUN_ID,
    'retrieval decision is explicit': () => payload !== null && payload.decision &&
      ['candidates', 'no_relevant_document'].includes(payload.decision.status),
    'returned results have citations': () => citationsValid,
  });
  retrievalFailed.add(!ok, { mode, task_type: evalCase.task_type });
  retrievalRateLimited.add(response.status === 429, { mode });
  retrievalTimedOut.add(response.status === 504, { mode });
  citationInvalid.add(payload !== null && Array.isArray(payload.results) && payload.results.length > 0 && !citationsValid, { mode });
  if (payload && payload.decision && payload.decision.status === 'no_relevant_document') {
    noRelevantDecisions.add(1, { mode, task_type: evalCase.task_type });
  }
}

export function handleSummary(data) {
  const markdown = renderSummary(data);
  return {
    '/results/summary.json': JSON.stringify(data, null, 2),
    '/results/summary.md': markdown,
    stdout: `${markdown}\n`,
  };
}

function buildScenarios(profile) {
  const common = {
    executor: 'constant-arrival-rate',
    timeUnit: '1s',
    preAllocatedVUs: positiveInt(__ENV.PREALLOCATED_VUS, 20),
    maxVUs: positiveInt(__ENV.MAX_VUS, 100),
    gracefulStop: __ENV.GRACEFUL_STOP || '15s',
    tags: { phase: 'phase7d', profile, retrieval_mode: MODE },
  };
  switch (profile) {
    case 'baseline':
      return { retrieval: { ...common, rate: positiveInt(__ENV.RATE, 2), duration: __ENV.DURATION || '30s' } };
    case 'step':
      return {
        retrieval: {
          ...common,
          executor: 'ramping-arrival-rate',
          startRate: positiveInt(__ENV.START_RPS, 1),
          stages: [
            { target: positiveInt(__ENV.STEP_LOW_RPS, 2), duration: __ENV.RAMP_DURATION || '10s' },
            { target: positiveInt(__ENV.STEP_LOW_RPS, 2), duration: __ENV.STEP_DURATION || '20s' },
            { target: positiveInt(__ENV.STEP_MID_RPS, 4), duration: __ENV.RAMP_DURATION || '10s' },
            { target: positiveInt(__ENV.STEP_MID_RPS, 4), duration: __ENV.STEP_DURATION || '20s' },
            { target: positiveInt(__ENV.STEP_HIGH_RPS, 6), duration: __ENV.RAMP_DURATION || '10s' },
            { target: positiveInt(__ENV.STEP_HIGH_RPS, 6), duration: __ENV.STEP_DURATION || '20s' },
            { target: 0, duration: __ENV.RAMP_DURATION || '10s' },
          ],
        },
      };
    case 'spike':
      return {
        retrieval: {
          ...common,
          executor: 'ramping-arrival-rate',
          startRate: 1,
          stages: [
            { target: positiveInt(__ENV.SPIKE_BASE_RPS, 2), duration: '10s' },
            { target: positiveInt(__ENV.SPIKE_RPS, 10), duration: '5s' },
            { target: positiveInt(__ENV.SPIKE_RPS, 10), duration: __ENV.SPIKE_DURATION || '15s' },
            { target: positiveInt(__ENV.SPIKE_BASE_RPS, 2), duration: '10s' },
            { target: 0, duration: '5s' },
          ],
        },
      };
    case 'soak':
      return { retrieval: { ...common, rate: positiveInt(__ENV.RATE, 2), duration: __ENV.DURATION || '10m' } };
    default:
      throw new Error(`unsupported PROFILE ${profile}`);
  }
}

function requestParams(mode, token) {
  const headers = { Accept: 'application/json', 'Content-Type': 'application/json' };
  if (token) headers.Authorization = `Bearer ${token}`;
  return {
    headers,
    timeout: __ENV.REQUEST_TIMEOUT || '10s',
    tags: { endpoint: 'retrieval_search', mode, kind: 'retrieval' },
  };
}

function loadCases() {
  try {
    return open(BENCHMARK_FILE)
      .trim()
      .split(/\r?\n/)
      .filter(Boolean)
      .map((line) => JSON.parse(line))
      .filter((item) => item.query && item.task_type);
  } catch (_) {
    return [];
  }
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
    throw new Error(`expected positive integer, got ${value}`);
  }
  return parsed;
}

function renderSummary(data) {
  const lines = [
    '# Phase 7 Retrieval Load Summary',
    '',
    `- Profile: ${PROFILE}`,
    `- Mode: ${MODE}`,
    `- Dataset version: ${DATASET_VERSION_ID}`,
    `- Ingestion run: ${INGESTION_RUN_ID}`,
    `- Public development cases: ${cases.length}`,
    `- Elapsed test time: ${((data.state && data.state.testRunDurationMs || 0) / 1000).toFixed(2)} s`,
    '',
    '| Metric | Count/Rate | P50 | P95 | P99 |',
    '| --- | ---: | ---: | ---: | ---: |',
  ];
  for (const metric of ['http_reqs', 'http_req_failed', 'http_req_duration', 'retrieval_failed',
    'retrieval_rate_limited', 'retrieval_timed_out', 'citation_invalid', 'retrieval_lexical_duration_ms', 'retrieval_vector_duration_ms',
    'retrieval_hybrid_duration_ms', 'dropped_iterations']) {
    lines.push(metricRow(data, metric));
  }
  lines.push('', `Threshold result: ${thresholdsPassed(data) ? 'PASS' : 'FAIL'}`);
  return lines.join('\n');
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
  return value === undefined || value === null ? 'n/a' : Number(value).toFixed(2);
}

function thresholdsPassed(data) {
  return Object.values(data.metrics).every((metric) => !metric.thresholds ||
    Object.values(metric.thresholds).every((threshold) => threshold.ok));
}
