import http from 'k6/http';
import { check, sleep } from 'k6';

export const options = {
  scenarios: {
    smoke: {
      executor: 'constant-vus',
      vus: 1,
      duration: '30s',
    },
  },
  thresholds: {
    http_req_failed: ['rate<0.01'],
    http_req_duration: ['p(95)<500'],
  },
};

const baseURL = __ENV.STREAMFORGE_API_URL || 'http://localhost:8080';

export default function () {
  for (const path of ['/health', '/ready', '/version', '/metrics']) {
    const response = http.get(`${baseURL}${path}`);
    check(response, {
      [`${path} responds successfully`]: (result) => result.status === 200,
    });
  }
  sleep(2.2);
}
