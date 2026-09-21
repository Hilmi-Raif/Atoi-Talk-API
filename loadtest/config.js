export const BASE_URL = __ENV.BASE_URL || 'http://127.0.0.1:8080';
export const WS_BASE_URL = __ENV.WS_BASE_URL || 'ws://127.0.0.1:8080';

const staticUsers = [
  { email: 'alice@atoitalk.local', password: 'Password123!', name: 'Alice' },
  { email: 'bob@atoitalk.local', password: 'Password123!', name: 'Bob' },
  { email: 'charlie@atoitalk.local', password: 'Password123!', name: 'Charlie' },
  { email: 'admin@atoitalk.local', password: 'Password123!', name: 'Admin' }
];

const loadUsers = [];
for (let i = 1; i <= 50; i++) {
  loadUsers.push({
    email: `loaduser${i}@atoitalk.local`,
    password: 'Password123!',
    name: `Load User ${i}`
  });
}

export const USERS = staticUsers.concat(loadUsers);

export const HEADERS = {
  'Content-Type': 'application/json'
};
