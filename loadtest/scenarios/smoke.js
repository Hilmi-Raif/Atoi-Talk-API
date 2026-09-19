import http from 'k6/http';
import { check, sleep } from 'k6';
import { BASE_URL, USERS } from '../config.js';
import { login, authHeaders } from '../helpers/auth.js';
import { recordResponse } from '../helpers/results.js';

export const options = {
  setupTimeout: '180s',
  scenarios: {
    smoke_test: {
      executor: 'constant-vus',
      vus: 5,
      duration: '10s'
    }
  },
  thresholds: {
    http_req_failed: ['rate<0.01'],
    loadtest_application_error_rate: ['rate<0.01'],
    loadtest_network_error_rate: ['rate<0.01'],
    http_req_duration: ['p(95)<200']
  }
};

export function setup() {
  const tokens = {};
  for (const user of USERS) {
    const auth = login(user.email, user.password);
    if (auth) {
      tokens[user.email] = auth.token;
    }
  }
  return { tokens };
}

export default function(data) {
  const resLivez = http.get(`${BASE_URL}/livez`);
  recordResponse(resLivez);
  check(resLivez, { 'livez is 200': (r) => r.status === 200 });

  const resReadyz = http.get(`${BASE_URL}/readyz`);
  recordResponse(resReadyz);
  check(resReadyz, { 'readyz is 200': (r) => r.status === 200 });

  const token = data.tokens['alice@atoitalk.local'];
  if (token) {
    const headers = authHeaders(token);
    const resChats = http.get(`${BASE_URL}/api/chats`, { headers });
    recordResponse(resChats);
    check(resChats, { 'get chats is 200': (r) => r.status === 200 });

    const resMe = http.get(`${BASE_URL}/api/user/current`, { headers });
    recordResponse(resMe);
    check(resMe, { 'get current user is 200': (r) => r.status === 200 });
  }

  sleep(0.5);
}
