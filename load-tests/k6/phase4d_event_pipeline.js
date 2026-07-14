import http from 'k6/http';
import { check, sleep } from 'k6';
import { Counter } from 'k6/metrics';

export const options = {
  vus: Number(__ENV.VUS || 5),
  duration: __ENV.DURATION || '30s',
  summaryTrendStats: ['min', 'avg', 'med', 'p(90)', 'p(95)', 'p(99)', 'max'],
  thresholds: {
    http_req_failed: ['rate<0.02'],
    http_req_duration: ['p(95)<800'],
    event_writes_accepted_total: ['count>0'],
  },
};

const BASE_URL = __ENV.BASE_URL || 'http://host.docker.internal:18080';
const TOKEN_FILE = __ENV.TOKEN_FILE || '/work/backend-go/tmp/dev_tokens.csv';
const NOTE_START = Number(__ENV.NOTE_START || 1);
const NOTE_COUNT = Number(__ENV.NOTE_COUNT || 5000);

const acceptedWrites = new Counter('event_writes_accepted_total');
const tokens = loadTokens();

export function setup() {
  if (tokens.length === 0) {
    throw new Error(`no dev tokens loaded from ${TOKEN_FILE}`);
  }
  const ready = http.get(`${BASE_URL}/ready`);
  if (ready.status !== 200) {
    throw new Error(`backend readiness failed: ${ready.status} ${ready.body}`);
  }
}

export default function () {
  const noteID = NOTE_START + ((__VU * 997 + __ITER) % NOTE_COUNT);
  const token = tokens[(__VU + __ITER) % tokens.length];
  const response = http.post(
    `${BASE_URL}/api/v1/notes/${noteID}/share`,
    JSON.stringify({ channel: 'phase4d_fault_test' }),
    {
      headers: {
        Authorization: `Bearer ${token}`,
        'Content-Type': 'application/json',
      },
      tags: { phase: 'phase4d', kind: 'event_write' },
    },
  );

  const accepted = check(response, {
    'share accepted while broker may be unavailable': (res) => res.status === 200,
  });
  if (accepted) {
    acceptedWrites.add(1);
  }
  sleep(0.2);
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
