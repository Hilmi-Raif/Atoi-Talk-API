import http from 'k6/http';
import { check } from 'k6';
import { BASE_URL, HEADERS } from '../config.js';

export function login(email, password, vu = 1, iter = 0) {
  const headers = {
    'Content-Type': 'application/json'
  };

  const payload = JSON.stringify({
    email: email,
    password: password,
    captcha_token: 'dev-dummy-token'
  });

  const res = http.post(`${BASE_URL}/api/auth/login`, payload, { headers: headers });
  
  if (res.status === 200) {
    try {
      const json = JSON.parse(res.body);
      if (json && json.data && json.data.token) {
        return {
          token: json.data.token,
          user: json.data.user
        };
      }
    } catch (e) {}
  }
  return null;
}

export function getTokens(users) {
  const tokens = [];
  for (let i = 0; i < users.length; i++) {
    const auth = login(users[i].email, users[i].password, i + 1, 0);
    if (auth && auth.token) {
      tokens.push({
        token: auth.token,
        user: auth.user,
        email: users[i].email,
        vuIndex: i,
      });
    }
  }
  return tokens;
}

export function authHeaders(token, vu = 1, iter = 0) {
  const headers = {
    'Content-Type': 'application/json'
  };
  if (token) {
    headers['Authorization'] = `Bearer ${token}`;
  }
  return headers;
}
