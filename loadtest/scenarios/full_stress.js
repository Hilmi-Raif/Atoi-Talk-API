import http from 'k6/http';
import { check, sleep, group } from 'k6';
import { Counter, Rate, Trend } from 'k6/metrics';
import { BASE_URL, USERS } from '../config.js';
import { getTokens, authHeaders } from '../helpers/auth.js';
import { recordResponse } from '../helpers/results.js';

export const fullScenarioDuration = new Trend('full_scenario_duration', true);
export const scenarioErrors = new Rate('scenario_errors');
export const completedOperations = new Counter('completed_operations');

export const options = {
  setupTimeout: '180s',
  scenarios: {
    stress_lifecycle: {
      executor: 'ramping-vus',
      startVUs: 5,
      stages: [
        { duration: '10s', target: 20 },
        { duration: '30s', target: 40 },
        { duration: '10s', target: 0 },
      ],
      gracefulRampDown: '10s',
    },
  },
  thresholds: {
    scenario_errors: ['rate<0.05'],
    loadtest_application_error_rate: ['rate<0.01'],
    loadtest_network_error_rate: ['rate<0.01'],
    http_req_duration: ['p(95)<1000', 'p(99)<2000'],
  },
};

const samplePngData = new Uint8Array([
  0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
  0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
  0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
  0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4,
  0x89, 0x00, 0x00, 0x00, 0x0a, 0x49, 0x44, 0x41,
  0x54, 0x78, 0x9c, 0x63, 0x00, 0x01, 0x00, 0x00,
  0x05, 0x00, 0x01, 0x0d, 0x0a, 0x2d, 0xb4, 0x00,
  0x00, 0x00, 0x00, 0x49, 0x45, 0x4e, 0x44, 0xae,
  0x42, 0x60, 0x82,
]).buffer;

export function setup() {
  const tokens = getTokens(USERS);
  return { tokens };
}

export default function (data) {
  if (!data || !data.tokens || data.tokens.length === 0) {
    sleep(1);
    return;
  }

  const vuIndex = (__VU - 1) >= 0 ? (__VU - 1) : 0;
  const userObj = data.tokens[vuIndex % data.tokens.length];
  const userToken = userObj.token ? userObj.token : userObj;
  const headers = authHeaders(userToken, vuIndex + 1, __ITER);
  const startScenario = Date.now();

  group('1. Profile & Settings Read', function () {
    const meRes = http.get(`${BASE_URL}/api/user/current`, { headers });
    recordResponse(meRes);
    const success = check(meRes, {
      'get me is 200': (r) => r.status === 200,
    });
    if (!success) scenarioErrors.add(1);
  });

  let targetChatID = null;
  group('2. Chat List & Message History', function () {
    const chatsRes = http.get(`${BASE_URL}/api/chats?limit=20`, { headers });
    recordResponse(chatsRes);
    const success = check(chatsRes, {
      'get chats is 200': (r) => r.status === 200,
    });

    if (chatsRes.status === 200) {
      try {
        const body = chatsRes.json();
        if (body && body.data && body.data.length > 0) {
          targetChatID = body.data[0].id;
        }
      } catch (e) {}
    }

    if (!success) {
      scenarioErrors.add(1);
      return;
    }

    if (targetChatID) {
      const msgsRes = http.get(`${BASE_URL}/api/chats/${targetChatID}/messages?limit=30`, { headers });
      recordResponse(msgsRes);
      check(msgsRes, {
        'get messages is 200': (r) => r.status === 200,
      });
    }
  });

  group('3. Send & Read Message Flow', function () {
    if (!targetChatID) return;

    const payload = JSON.stringify({
      chat_id: targetChatID,
      content: `Stress test message VU ${__VU} ITER ${__ITER} at ${Date.now()}`,
    });

    const sendRes = http.post(`${BASE_URL}/api/messages`, payload, { headers });
    recordResponse(sendRes);
    const sendSuccess = check(sendRes, {
      'send message is 200': (r) => r.status === 200 || r.status === 201,
    });

    if (sendSuccess) {
      completedOperations.add(1);
    } else {
      scenarioErrors.add(1);
    }
  });

  if (__ITER % 5 === 0) {
    group('4. Media Upload & Attachment Flow', function () {
      const now = Date.now();
      const fileName = `stress_avatar_${__VU}_${__ITER}_${now}.png`;
      const initRes = http.post(
        `${BASE_URL}/api/media/upload`,
        JSON.stringify({
          original_name: fileName,
          file_size: samplePngData.byteLength,
          mime_type: 'image/png',
          usage: 'user_avatar',
          captcha_token: 'dev-dummy-token',
        }),
        { headers }
      );
      recordResponse(initRes);

      if (initRes.status === 200 && initRes.json().data && initRes.json().data.media) {
        const uploadData = initRes.json().data;
        const mediaID = uploadData.media.id;
        const putRes = http.put(uploadData.upload_url, samplePngData, {
          headers: { 'Content-Type': 'image/png' },
        });
        recordResponse(putRes);

          if (putRes.status === 200 || putRes.status === 204) {
            const compRes = http.post(`${BASE_URL}/api/media/${mediaID}/complete`, null, { headers });
            recordResponse(compRes);
          if (compRes.status === 200) {
            completedOperations.add(1);
          }
        }
      }
    });
  }

  fullScenarioDuration.add(Date.now() - startScenario);
  sleep(3);
}
