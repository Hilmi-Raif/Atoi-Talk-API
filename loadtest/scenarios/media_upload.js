import http from 'k6/http';
import { check, sleep } from 'k6';
import { Counter, Rate, Trend } from 'k6/metrics';
import { BASE_URL, USERS } from '../config.js';
import { getTokens, authHeaders } from '../helpers/auth.js';
import { recordResponse } from '../helpers/results.js';

export const uploadDuration = new Trend('upload_duration', true);
export const s3PutDuration = new Trend('s3_put_duration', true);
export const completeDuration = new Trend('complete_duration', true);
export const uploadErrors = new Rate('upload_errors');
export const completedUploads = new Counter('completed_uploads');

export const options = {
  setupTimeout: '180s',
  scenarios: {
    media_upload_stress: {
      executor: 'per-vu-iterations',
      vus: 50,
      iterations: 2,
      maxDuration: '30s',
    },
  },
  thresholds: {
    upload_errors: ['rate<0.05'],
    loadtest_application_error_rate: ['rate<0.01'],
    loadtest_network_error_rate: ['rate<0.01'],
    http_req_duration: ['p(95)<1500'],
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
    uploadErrors.add(1);
    sleep(1);
    return;
  }

  const vuIndex = (__VU - 1) >= 0 ? (__VU - 1) : 0;
  const userObj = data.tokens[vuIndex % data.tokens.length];
  const token = userObj.token ? userObj.token : userObj;
  const headers = {
    'Content-Type': 'application/json',
    'Authorization': `Bearer ${token}`
  };

  const now = Date.now();
  const fileName = `loadtest_${__VU}_${__ITER}_${now}.png`;

  const initStart = Date.now();
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

  const initSuccess = check(initRes, {
    'upload init status is 200': (r) => r.status === 200,
    'upload init returns upload_url': (r) => {
      const body = r.json();
      return body && body.data && body.data.upload_url && body.data.media && body.data.media.id;
    },
  });

  if (!initSuccess) {
    uploadErrors.add(1);
    sleep(0.5);
    return;
  }

  uploadDuration.add(Date.now() - initStart);
  const uploadInfo = initRes.json().data;
  const mediaID = uploadInfo.media.id;

  const s3Start = Date.now();
  const putRes = http.put(
    uploadInfo.upload_url,
    samplePngData,
    {
      headers: {
        'Content-Type': 'image/png',
      },
    }
  );
  recordResponse(putRes);

  const putSuccess = check(putRes, {
    's3 put status is 200': (r) => r.status === 200 || r.status === 204,
  });

  if (!putSuccess) {
    uploadErrors.add(1);
    sleep(0.5);
    return;
  }

  s3PutDuration.add(Date.now() - s3Start);

  const completeStart = Date.now();
  const completeRes = http.post(
    `${BASE_URL}/api/media/${mediaID}/complete`,
    null,
    { headers }
  );
  recordResponse(completeRes);

  const completeSuccess = check(completeRes, {
    'complete status is 200': (r) => r.status === 200,
    'complete status is completed': (r) => {
      const body = r.json();
      return body && body.data && body.data.upload_status === 'completed';
    },
  });

  if (!completeSuccess) {
    uploadErrors.add(1);
    sleep(0.5);
    return;
  }

  completeDuration.add(Date.now() - completeStart);
  completedUploads.add(1);
  uploadErrors.add(0);

  sleep(0.5);
}
