import { Counter, Rate } from 'k6/metrics';

export const successfulResponses = new Counter('loadtest_successful_responses');
export const rateLimitedResponses = new Counter('loadtest_rate_limited_responses');
export const serverErrorResponses = new Counter('loadtest_server_error_responses');
export const networkErrorResponses = new Counter('loadtest_network_error_responses');
export const applicationErrorRate = new Rate('loadtest_application_error_rate');
export const networkErrorRate = new Rate('loadtest_network_error_rate');

export function recordResponse(response) {
  const status = response && typeof response.status === 'number' ? response.status : 0;
  const isNetworkError = status === 0;
  const isRateLimited = status === 429;
  const isServerError = status >= 500;
  const isSuccessful = status >= 200 && status < 400;
  const isApplicationError = status >= 400 && !isRateLimited;

  successfulResponses.add(isSuccessful ? 1 : 0);
  rateLimitedResponses.add(isRateLimited ? 1 : 0);
  serverErrorResponses.add(isServerError ? 1 : 0);
  networkErrorResponses.add(isNetworkError ? 1 : 0);
  applicationErrorRate.add(isApplicationError);
  networkErrorRate.add(isNetworkError);
}
