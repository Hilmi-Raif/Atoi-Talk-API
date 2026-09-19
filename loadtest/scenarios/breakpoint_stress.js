import http from 'k6/http';
import { check, sleep } from 'k6';
import { Counter, Rate, Trend } from 'k6/metrics';
import { BASE_URL, USERS } from '../config.js';
import { login, authHeaders } from '../helpers/auth.js';
import { recordResponse } from '../helpers/results.js';

const errorRate = new Rate('custom_error_rate');
const msgLatency = new Trend('message_send_latency');
const chatLatency = new Trend('chat_list_latency');
const msgCounter = new Counter('messages_sent_total');
const maxRate = Number(__ENV.MAX_RPS || 900);
const rateTargets = [100, 250, 500, 700, 900, 1000, 1200].filter((target) => target <= maxRate);

export const options = {
  setupTimeout: '180s',
  scenarios: {
    breakpoint_stress: {
      executor: 'ramping-arrival-rate',
      startRate: 50,
      timeUnit: '1s',
      preAllocatedVUs: 50,
      maxVUs: 500,
      stages: [
        ...rateTargets.map((target) => ({ target, duration: '20s' })),
        { target: 0, duration: '10s' }
      ]
    }
  },
  thresholds: {
    loadtest_application_error_rate: ['rate<0.01'],
    loadtest_network_error_rate: ['rate<0.01'],
    http_req_duration: ['p(95)<500', 'p(99)<1000']
  }
};

export function setup() {
  const users = {};
  for (const u of USERS) {
    const auth = login(u.email, u.password);
    if (auth) {
      users[u.email] = {
        token: auth.token,
        user: auth.user
      };
    }
  }

  const aliceToken = users['alice@atoitalk.local'].token;
  const chatsRes = http.get(`${BASE_URL}/api/chats`, { headers: authHeaders(aliceToken) });
  recordResponse(chatsRes);
  let groupChatID = null;
  let privateChatID = null;

  if (chatsRes.status === 200) {
    const json = JSON.parse(chatsRes.body);
    if (json.data && json.data.length > 0) {
      for (const c of json.data) {
        if (c.type === 'group' && !groupChatID) {
          groupChatID = c.id;
        }
        if (c.type === 'private' && !privateChatID) {
          privateChatID = c.id;
        }
      }
    }
  }

  return {
    users,
    groupChatID,
    privateChatID
  };
}

export default function(data) {
  const userKeys = ['alice@atoitalk.local', 'bob@atoitalk.local', 'charlie@atoitalk.local'];
  const randomUser = userKeys[Math.floor(Math.random() * userKeys.length)];
  const token = data.users[randomUser] ? data.users[randomUser].token : null;

  if (!token) {
    errorRate.add(1);
    return;
  }

  const headers = authHeaders(token);
  const action = Math.random();

  if (action < 0.40) {
    const start = Date.now();
    const res = http.get(`${BASE_URL}/api/chats`, { headers });
    recordResponse(res);
    chatLatency.add(Date.now() - start);

    const ok = check(res, {
      'get chats 200': (r) => r.status === 200
    });
    errorRate.add(!ok);
  } else if (action < 0.70 && data.groupChatID) {
    const start = Date.now();
    const payload = JSON.stringify({
      chat_id: data.groupChatID,
      content: `Stress msg from VU-${__VU} iter-${__ITER} at ${Date.now()}`
    });

    const res = http.post(`${BASE_URL}/api/messages`, payload, { headers });
    recordResponse(res);
    msgLatency.add(Date.now() - start);

    const ok = check(res, {
      'send group msg 200 or 201': (r) => r.status === 200 || r.status === 201
    });

    if (ok) {
      msgCounter.add(1);
    }
    errorRate.add(!ok);
  } else if (action < 0.90 && data.groupChatID) {
    const res = http.get(`${BASE_URL}/api/chats/${data.groupChatID}/messages?limit=30`, { headers });
    recordResponse(res);
    const ok = check(res, {
      'get messages 200': (r) => r.status === 200
    });
    errorRate.add(!ok);
  } else {
    const res = http.get(`${BASE_URL}/api/users?query=a&limit=20`, { headers });
    recordResponse(res);
    const ok = check(res, {
      'search users 200': (r) => r.status === 200
    });
    errorRate.add(!ok);
  }
}
