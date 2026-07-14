import http from 'k6/http';
import { check, sleep } from 'k6';
import { Counter } from 'k6/metrics';

export const options = {
  vus: Number(__ENV.VUS || 20),
  duration: __ENV.DURATION || '1m',
  summaryTrendStats: ['min', 'avg', 'med', 'p(90)', 'p(95)', 'p(99)', 'max'],
  thresholds: {
    http_req_failed: ['rate<0.05'],
    http_req_duration: ['p(95)<800'],
  },
};

const BASE_URL = __ENV.BASE_URL || 'http://127.0.0.1:18080';
const TOKEN_FILE = __ENV.TOKEN_FILE || '';
const NOTE_START = Number(__ENV.NOTE_START || 1);
const NOTE_COUNT = Number(__ENV.NOTE_COUNT || 5000);
const COMMENT_START = Number(__ENV.COMMENT_START || 1);
const COMMENT_COUNT = Number(__ENV.COMMENT_COUNT || 20000);

const writeSkipped = new Counter('write_skipped_no_token_total');
const tokens = loadTokens();

export default function () {
  const noteID = NOTE_START + ((__VU * 997 + __ITER) % NOTE_COUNT);
  const commentID = COMMENT_START + ((__VU * 1499 + __ITER) % COMMENT_COUNT);
  const token = pickToken();

  const readParams = { tags: { phase: 'phase3', kind: 'read' } };
  check(http.get(`${BASE_URL}/api/v1/notes/${noteID}`, readParams), {
    'note detail ok': (res) => res.status === 200,
  });
  check(http.get(`${BASE_URL}/api/v1/notes/${noteID}/comments?limit=20`, readParams), {
    'comments ok': (res) => res.status === 200,
  });
  check(http.get(`${BASE_URL}/api/v1/rankings/notes/daily?limit=20`, readParams), {
    'hot notes ok': (res) => res.status === 200,
  });

  if (!token) {
    writeSkipped.add(1);
    sleep(1);
    return;
  }

  const writeParams = {
    headers: {
      Authorization: `Bearer ${token}`,
      'Content-Type': 'application/json',
    },
    tags: { phase: 'phase3', kind: 'write' },
  };

  check(http.post(`${BASE_URL}/api/v1/notes/${noteID}/like`, '{}', writeParams), {
    'like accepted': (res) => res.status === 200,
  });
  check(http.post(`${BASE_URL}/api/v1/notes/${noteID}/collect`, JSON.stringify({ collection_name: 'k6-dev' }), writeParams), {
    'collect accepted': (res) => res.status === 200,
  });

  const commentRes = http.post(
    `${BASE_URL}/api/v1/notes/${noteID}/comments`,
    JSON.stringify({ content: `k6 phase3 comment ${__VU}-${__ITER}`, intent: 'load_test' }),
    writeParams,
  );
  check(commentRes, {
    'comment created': (res) => res.status === 201,
  });

  let createdCommentID = commentID;
  if (commentRes.status === 201) {
    try {
      createdCommentID = JSON.parse(commentRes.body).id || commentID;
    } catch (_) {
      createdCommentID = commentID;
    }
  }
  check(http.post(`${BASE_URL}/api/v1/comments/${createdCommentID}/like`, '{}', writeParams), {
    'comment like accepted': (res) => res.status === 200,
  });

  sleep(1);
}

function loadTokens() {
  const candidates = TOKEN_FILE
    ? [TOKEN_FILE]
    : ['/work/backend-go/tmp/dev_tokens.csv', '../../backend-go/tmp/dev_tokens.csv', 'backend-go/tmp/dev_tokens.csv'];

  for (const candidate of candidates) {
    try {
      const csv = open(candidate);
      const loaded = csv
        .trim()
        .split(/\r?\n/)
        .slice(1)
        .map((line) => line.split(',')[1])
        .filter(Boolean);
      if (loaded.length > 0) {
        return loaded;
      }
    } catch (_) {
      // Try the next path. Docker and local k6 resolve script-relative paths differently.
    }
  }
  return [];
}

function pickToken() {
  if (tokens.length === 0) {
    return '';
  }
  return tokens[(__VU + __ITER) % tokens.length];
}
