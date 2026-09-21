import http from 'k6/http';
import { check } from 'k6';
import { Counter, Rate, Trend } from 'k6/metrics';
import exec from 'k6/execution';
import { BASE_URL, USERS } from '../config.js';
import { login, authHeaders } from '../helpers/auth.js';
import { recordResponse } from '../helpers/results.js';

const applicationErrors = new Rate('steady_application_errors');
const completedActions = new Counter('steady_completed_actions');
const actionLatency = new Trend('steady_action_latency');
const chatActions = new Counter('steady_chat_actions');
const messageSendActions = new Counter('steady_message_send_actions');
const messageListActions = new Counter('steady_message_list_actions');
const userSearchActions = new Counter('steady_user_search_actions');
const targetRate = Number(__ENV.TARGET_RPS || 1000);
const duration = __ENV.DURATION || '60s';

export const options = {
  setupTimeout: '180s',
  scenarios: {
    steady_breakpoint: {
      executor: 'constant-arrival-rate',
      rate: targetRate,
      timeUnit: '1s',
      duration,
      preAllocatedVUs: Math.min(Math.max(targetRate, 50), 1000),
      maxVUs: Math.min(Math.max(targetRate * 3, 100), 3000),
      gracefulStop: '10s'
    }
  },
  thresholds: {
    steady_application_errors: ['rate<0.01'],
    loadtest_application_error_rate: ['rate<0.01'],
    loadtest_network_error_rate: ['rate<0.01'],
    http_req_duration: ['p(95)<500', 'p(99)<1000']
  }
};

export function setup() {
  const users = [];
  for (const candidate of USERS) {
    const auth = login(candidate.email, candidate.password);
    if (auth && auth.token) {
      users.push({ token: auth.token, email: candidate.email });
    }
  }

  if (users.length === 0) {
    throw new Error('no load test users authenticated');
  }

  let groupChatID = null;
  const groupUsers = [];
  for (const user of users) {
    const chatsResponse = http.get(`${BASE_URL}/api/chats`, {
      headers: authHeaders(user.token)
    });
    recordResponse(chatsResponse);
    if (chatsResponse.status !== 200) {
      continue;
    }

    const payload = JSON.parse(chatsResponse.body);
    for (const chat of payload.data || []) {
      if (chat.type === 'group' && !groupChatID) {
        groupChatID = chat.id;
      }
      if (chat.type === 'group' && chat.id === groupChatID) {
        groupUsers.push(user);
        break;
      }
    }
  }

  if (!groupChatID || groupUsers.length === 0) {
    throw new Error('no group chat available for steady breakpoint test');
  }

  return { users: groupUsers, groupChatID };
}

export default function(data) {
  const user = data.users[(__VU - 1) % data.users.length];
  const headers = authHeaders(user.token);
  const action = exec.scenario.iterationInTest % 4;
  const startedAt = Date.now();
  let response;
  let ok;

  if (action === 0) {
    chatActions.add(1);
    response = http.get(`${BASE_URL}/api/chats`, { headers });
    ok = check(response, { 'steady chats succeeded': (r) => r.status === 200 });
  } else if (action === 1) {
    messageSendActions.add(1);
    response = http.post(`${BASE_URL}/api/messages`, JSON.stringify({
      chat_id: data.groupChatID,
      content: `steady message ${__VU}-${__ITER}`
    }), { headers });
    ok = check(response, {
      'steady message succeeded': (r) => r.status === 200 || r.status === 201
    });
  } else if (action === 2) {
    messageListActions.add(1);
    response = http.get(`${BASE_URL}/api/chats/${data.groupChatID}/messages?limit=30`, { headers });
    ok = check(response, { 'steady messages succeeded': (r) => r.status === 200 });
  } else {
    userSearchActions.add(1);
    response = http.get(`${BASE_URL}/api/users?query=a&limit=20`, { headers });
    ok = check(response, { 'steady users succeeded': (r) => r.status === 200 });
  }

  recordResponse(response);
  actionLatency.add(Date.now() - startedAt);
  applicationErrors.add(!ok);
  if (ok) {
    completedActions.add(1);
  }
}
