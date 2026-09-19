import http from 'k6/http';
import { check } from 'k6';
import { Counter, Rate } from 'k6/metrics';
import { BASE_URL, USERS } from '../config.js';
import { login, authHeaders } from '../helpers/auth.js';

const applicationErrors = new Rate('isolated_application_errors');
const completedActions = new Counter('isolated_completed_actions');
const targetRate = Number(__ENV.TARGET_RPS || 100);
const duration = __ENV.DURATION || '30s';
const endpoint = __ENV.ENDPOINT || 'chats';
const maxVUs = Number(__ENV.MAX_VUS || Math.min(Math.max(targetRate * 3, 100), 3000));
const preAllocatedVUs = Number(__ENV.PREALLOCATED_VUS || maxVUs);
const aroundMessageID = (__ENV.AROUND_MESSAGE_ID || '').trim();
const attachmentIDs = (__ENV.ATTACHMENT_IDS || '')
  .split(',')
  .map((value) => value.trim())
  .filter(Boolean);
const configuredChatIDs = (__ENV.CHAT_IDS || '')
  .split(',')
  .map((value) => value.trim())
  .filter(Boolean);

export const options = {
  setupTimeout: '180s',
  scenarios: {
    isolated_endpoint: {
      executor: 'constant-arrival-rate',
      rate: targetRate,
      timeUnit: '1s',
      duration,
      preAllocatedVUs: Math.min(Math.max(preAllocatedVUs, 1), maxVUs),
      maxVUs,
      gracefulStop: '10s'
    }
  },
  thresholds: {
    dropped_iterations: ['count==0'],
    isolated_application_errors: ['rate<0.01'],
    http_req_duration: ['p(95)<500', 'p(99)<1000']
  }
};

function loadUsers() {
  const users = [];
  for (const candidate of USERS) {
    const auth = login(candidate.email, candidate.password);
    if (auth && auth.token) {
      users.push({ token: auth.token });
    }
  }
  if (users.length === 0) {
    throw new Error('no load test users authenticated');
  }
  return users;
}

function findGroupChat(users) {
  const groupUsers = [];
  let groupChatID = null;

  for (const user of users) {
    const response = http.get(`${BASE_URL}/api/chats`, {
      headers: authHeaders(user.token)
    });
    if (response.status !== 200) {
      continue;
    }
    const payload = JSON.parse(response.body);
    const group = (payload.data || []).find((chat) => chat.type === 'group' && (!groupChatID || chat.id === groupChatID));
    if (group && !groupChatID) {
      groupChatID = group.id;
    }
    if (group && group.id === groupChatID) {
      groupUsers.push(user);
    }
  }

  if (!groupChatID || groupUsers.length === 0) {
    throw new Error('no group chat available for isolated endpoint test');
  }

  return { groupUsers, groupChatID };
}

function findConfiguredGroupChats(users) {
  const groupUsers = users.filter((user) => {
    const response = http.get(`${BASE_URL}/api/chats`, {
      headers: authHeaders(user.token)
    });
    if (response.status !== 200) {
      return false;
    }

    const payload = JSON.parse(response.body);
    const visibleGroupChatIDs = new Set(
      (payload.data || [])
        .filter((chat) => chat.type === 'group')
        .map((chat) => chat.id)
    );
    return configuredChatIDs.every((chatID) => visibleGroupChatIDs.has(chatID));
  });

  if (configuredChatIDs.length === 0 || groupUsers.length === 0) {
    throw new Error('configured chat fixture is unavailable to load users');
  }

  return { users: groupUsers, groupChatIDs: configuredChatIDs };
}

export function setup() {
  const users = loadUsers();
  if (configuredChatIDs.length > 0) {
    return findConfiguredGroupChats(users);
  }

  const group = findGroupChat(users);
  return { users: group.groupUsers, groupChatIDs: [group.groupChatID] };
}

export default function(data) {
  const user = data.users[(__VU - 1) % data.users.length];
  const chatID = data.groupChatIDs[(__VU - 1) % data.groupChatIDs.length];
  const headers = authHeaders(user.token);
  let response;

  if (endpoint === 'chats') {
    response = http.get(`${BASE_URL}/api/chats`, { headers });
  } else if (endpoint === 'messages') {
    response = http.get(`${BASE_URL}/api/chats/${chatID}/messages?limit=30`, { headers });
  } else if (endpoint === 'around') {
    if (!aroundMessageID) {
      throw new Error('AROUND_MESSAGE_ID is required for ENDPOINT=around');
    }
    response = http.get(`${BASE_URL}/api/chats/${chatID}/messages?around_message_id=${aroundMessageID}&limit=30`, { headers });
  } else if (endpoint === 'send') {
    const request = {
      chat_id: chatID,
      content: `isolated message ${__VU}-${__ITER}`
    };
    if (attachmentIDs.length > 0) {
      request.attachment_ids = attachmentIDs;
      request.content = '';
    }
    response = http.post(`${BASE_URL}/api/messages`, JSON.stringify(request), { headers });
  } else if (endpoint === 'users') {
    response = http.get(`${BASE_URL}/api/users?query=a&limit=20`, { headers });
  } else if (endpoint === 'media') {
    response = http.post(`${BASE_URL}/api/media/upload`, JSON.stringify({
      usage: 'message_attachment',
      original_name: `bench_${__VU}_${__ITER}.png`,
      mime_type: 'image/png',
      file_size: 1024,
      captcha_token: 'dev-dummy-token'
    }), { headers });
  } else {
    throw new Error(`unsupported ENDPOINT: ${endpoint}`);
  }

  const ok = endpoint === 'send' || endpoint === 'media'
    ? check(response, { 'isolated write/presign succeeded': (r) => r.status === 200 || r.status === 201 })
    : check(response, { 'isolated request succeeded': (r) => r.status === 200 });
  applicationErrors.add(!ok);
  if (ok) {
    completedActions.add(1);
  }
}
