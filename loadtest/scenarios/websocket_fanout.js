import http from 'k6/http';
import ws from 'k6/ws';
import { check, sleep } from 'k6';
import { Counter, Rate, Trend } from 'k6/metrics';
import { BASE_URL, WS_BASE_URL, USERS } from '../config.js';
import { authHeaders, login } from '../helpers/auth.js';

const fanoutMessagesSent = new Counter('fanout_messages_sent');
const fanoutMessagesReceived = new Counter('fanout_messages_received');
const fanoutErrors = new Rate('fanout_errors');
const fanoutDelivery = new Trend('fanout_delivery_duration', true);

const duration = __ENV.DURATION || '30s';
const listenerDuration = __ENV.LISTENER_DURATION || '40s';
const sendRate = Number(__ENV.SEND_RPS || 1);
const listenerVUs = Number(__ENV.LISTENER_VUS || 2);
const listenerSessionSeconds = Number(__ENV.LISTENER_SESSION_SECONDS || 45);
const chatID = (__ENV.CHAT_ID || '00000000-0000-0000-0000-000000000001').trim();
const senderEmail = __ENV.SENDER_EMAIL || 'alice@atoitalk.local';
const listenerEmails = (__ENV.LISTENER_EMAILS || 'bob@atoitalk.local,charlie@atoitalk.local')
  .split(',')
  .map((value) => value.trim())
  .filter(Boolean);

export const options = {
  setupTimeout: '180s',
  scenarios: {
    websocket_listeners: {
      executor: 'constant-vus',
      vus: listenerVUs,
      duration: listenerDuration,
      exec: 'listen',
      gracefulStop: '5s'
    },
    api_senders: {
      executor: 'constant-arrival-rate',
      startTime: '5s',
      rate: sendRate,
      timeUnit: '1s',
      duration,
      preAllocatedVUs: Math.max(1, Math.min(sendRate, 10)),
      maxVUs: Math.max(10, sendRate * 3),
      exec: 'send',
      gracefulStop: '5s'
    }
  },
  thresholds: {
    fanout_errors: ['rate<0.01'],
    fanout_delivery_duration: ['p(95)<1000']
  }
};

function findUser(email) {
  const candidate = USERS.find((user) => user.email === email);
  if (!candidate) {
    throw new Error(`unknown benchmark user: ${email}`);
  }

  const auth = login(candidate.email, candidate.password);
  if (!auth || !auth.token) {
    throw new Error(`benchmark login failed: ${email}`);
  }

  return { email, token: auth.token };
}

export function setup() {
  const sender = findUser(senderEmail);
  const listeners = listenerEmails.map(findUser);
  const runID = `fanout-${Date.now()}`;

  const access = http.get(`${BASE_URL}/api/chats/${chatID}/messages?limit=1`, {
    headers: authHeaders(sender.token)
  });
  if (access.status !== 200) {
    throw new Error(`sender cannot access benchmark chat: ${chatID}`);
  }

  for (const listener of listeners) {
    const listenerAccess = http.get(`${BASE_URL}/api/chats/${chatID}/messages?limit=1`, {
      headers: authHeaders(listener.token)
    });
    if (listenerAccess.status !== 200) {
      throw new Error(`listener cannot access benchmark chat: ${listener.email}`);
    }
  }

  return { sender, listeners, runID };
}

export function listen(data) {
  const listener = data.listeners[(__VU - 1) % data.listeners.length];
  const start = Date.now();
  const response = ws.connect(`${WS_BASE_URL}/ws?token=${listener.token}`, {}, function(socket) {
    socket.on('message', function(message) {
      try {
        const event = JSON.parse(message);
        if (event.type !== 'message.new' || (event.payload?.content !== undefined && !String(event.payload.content).startsWith(`fanout benchmark ${data.runID} `))) {
          return;
        }

        const sentAt = Number(event.meta?.timestamp || 0);
        if (sentAt > 0) {
          fanoutDelivery.add(Math.max(0, Date.now() - sentAt));
        } else {
          fanoutDelivery.add(Date.now() - start);
        }
        fanoutMessagesReceived.add(1);
      } catch (error) {
        fanoutErrors.add(1);
      }
    });

    socket.on('error', function() {
      fanoutErrors.add(1);
    });

    socket.setInterval(function() {
      socket.ping();
    }, 5000);

    socket.setTimeout(function() {
      socket.close();
    }, listenerSessionSeconds * 1000);
  });

  const connected = check(response, {
    'fanout websocket connected': (result) => result && result.status === 101
  });
  fanoutErrors.add(!connected);
  sleep(1);
}

export function send(data) {
  const message = http.post(
    `${BASE_URL}/api/messages`,
    JSON.stringify({
      chat_id: chatID,
      content: `fanout benchmark ${data.runID} ${__VU}-${__ITER}`
    }),
    { headers: authHeaders(data.sender.token) }
  );
  const sent = check(message, {
    'fanout message accepted': (response) => response.status === 200 || response.status === 201
  });
  fanoutErrors.add(!sent);
  if (sent) {
    fanoutMessagesSent.add(1);
  }
}
