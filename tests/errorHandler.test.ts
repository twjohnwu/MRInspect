import { ErrorHandler } from '../src/error/ErrorHandler';
import { ValidationError } from '../src/types';
import type { ErrorReportingConfig } from '../src/types';

function makeHandler(postToMR = true): ErrorHandler {
  const cfg: ErrorReportingConfig = { postToMR, maxMessageLength: 2000 };
  return new ErrorHandler(cfg);
}

describe('ErrorHandler.categorize', () => {
  const h = makeHandler();

  test('ValidationError maps to validation', () => {
    expect(h.categorize(new ValidationError('CODE', 'bad input'))).toBe('validation');
  });

  test('missing credentials maps to configuration', () => {
    expect(h.categorize(new Error('missing required ai_provider_key'))).toBe('configuration');
  });

  test('rate limit maps to api', () => {
    expect(h.categorize(new Error('HTTP 429 rate limit exceeded'))).toBe('api');
  });

  test('timeout maps to api', () => {
    expect(h.categorize(new Error('request timeout after 30s'))).toBe('api');
  });

  test('git error maps to system', () => {
    expect(h.categorize(new Error('fatal: git repository not found'))).toBe('system');
  });

  test('unknown error maps to unknown', () => {
    expect(h.categorize(new Error('something weird'))).toBe('unknown');
  });
});

describe('ErrorHandler.shouldPost', () => {
  test('returns false when postToMR is false', () => {
    expect(makeHandler(false).shouldPost(new Error('any'))).toBe(false);
  });

  test('returns false for rate-limit errors', () => {
    expect(makeHandler().shouldPost(new Error('HTTP 429 rate limit'))).toBe(false);
  });

  test('returns true for normal errors', () => {
    expect(makeHandler().shouldPost(new Error('something broke'))).toBe(true);
  });
});

describe('ErrorHandler.generateMessage', () => {
  test('message contains stage and error text', () => {
    const h = makeHandler();
    const msg = h.generateMessage(new Error('oops'), 'fetchMRDetails', 'api');
    expect(msg).toContain('fetching MR details');
    expect(msg).toContain('oops');
    expect(msg).toContain('api');
  });

  test('message includes troubleshooting steps for configuration', () => {
    const h = makeHandler();
    const msg = h.generateMessage(new Error('missing token'), 'validateSystem', 'configuration');
    expect(msg).toContain('GITLAB_TOKEN');
  });

  test('truncates very long error messages', () => {
    const cfg: ErrorReportingConfig = { postToMR: true, maxMessageLength: 20 };
    const h = new ErrorHandler(cfg);
    const msg = h.generateMessage(new Error('x'.repeat(100)), 'postReview', 'unknown');
    expect(msg).toContain('...');
  });
});
