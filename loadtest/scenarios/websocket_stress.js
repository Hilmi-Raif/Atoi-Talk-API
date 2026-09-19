import ws from 'k6/ws';
import { check, sleep } from 'k6';
import { Counter, Rate, Trend } from 'k6/metrics';
import { WS_BASE_URL, USERS } from '../config.js';
import { login } from '../helpers/auth.js';

const wsConnections = new Counter('ws_connections_total');
const wsErrors = new Rate('ws_errors_rate');
const wsMessageReceived = new Counter('ws_messages_received');

export const options = {
  setupTimeout: '180s',
  scenarios: {
    concurrent_ws: {
      executor: 'ramping-vus',
      startVUs: 10,
      stages: [
        { duration: '10s', target: 50 },
        { duration: '15s', target: 150 },
        { duration: '15s', target: 300 },
        { duration: '10s', target: 0 }
      ]
    }
  },
  thresholds: {
    ws_errors_rate: ['rate<0.05']
  }
};

export function setup() {
  const tokens = {};
  for (const u of USERS) {
    const auth = login(u.email, u.password);
    if (auth) {
      tokens[u.email] = auth.token;
    }
  }
  return { tokens };
}

export default function(data) {
  const userKeys = ['alice@atoitalk.local', 'bob@atoitalk.local', 'charlie@atoitalk.local'];
  const randomUser = userKeys[__VU % userKeys.length];
  const token = data.tokens[randomUser];

  if (!token) {
    wsErrors.add(1);
    return;
  }

  const url = `${WS_BASE_URL}/ws?token=${token}`;

  const res = ws.connect(url, {}, function(socket) {
    wsConnections.add(1);

    socket.on('open', function() {
      socket.setInterval(function() {
        socket.ping();
      }, 5000);
    });

    socket.on('message', function(msg) {
      wsMessageReceived.add(1);
    });

    socket.on('error', function(e) {
      wsErrors.add(1);
    });

    socket.setTimeout(function() {
      socket.close();
    }, 5000);
  });

  check(res, { 'ws connected successfully': (r) => r && r.status === 101 });
  sleep(1);
}
