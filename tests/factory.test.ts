import { createReviewer } from '../src/factory';
import { MRReviewer } from '../src/review/MRReviewer';
import type { Config } from '../src/types';

function makeConfig(aiProvider: Config['aiProvider'] = 'anthropic'): Config {
  return {
    aiProvider,
    aiProviderKey:   'test-key',
    selfReflection:  false,
    service:         { name: 'test-service', type: 'backend' },
    crossRepo:       { enabled: false },
    gitlab: {
      apiBase:         'https://gitlab.example.com/api/v4',
      token:           'test-token',
      timeoutMs:       5000,
      retryAttempts:   1,
      retryDelayMs:    100,
      maxRetryDelayMs: 1000,
    },
    providers: {
      anthropic: { model: 'claude-3-5-sonnet-20241022', maxTokens: 4000, temperature: 0.1 },
      gemini:    { model: 'gemini-2.5-pro',             maxTokens: 8000, temperature: 0.1 },
      openai:    { model: 'gpt-5',                      maxTokens: 4000, temperature: 0.1 },
    },
    projects: {
      directory:    '../projects',
      registryFile: '../projects/registry.yaml',
      sharedDir:    '../projects/_shared',
    },
    validation: {
      allowedExtensions: ['.ts', '.js'],
      maxFilesChanged:   50,
      maxDiffSizeKB:     300,
      aiRetryAttempts:   3,
    },
    errorReporting: { postToMR: true, maxMessageLength: 2000 },
    metricsFile: './metrics.json',
    logLevel:    'info',
  };
}

describe('createReviewer', () => {
  test('returns an MRReviewer instance', () => {
    const reviewer = createReviewer(makeConfig('anthropic'));
    expect(reviewer).toBeInstanceOf(MRReviewer);
  });

  test('creates reviewer for anthropic provider', () => {
    expect(() => createReviewer(makeConfig('anthropic'))).not.toThrow();
  });

  test('creates reviewer for gemini provider', () => {
    expect(() => createReviewer(makeConfig('gemini'))).not.toThrow();
  });

  test('creates reviewer for openai provider', () => {
    expect(() => createReviewer(makeConfig('openai'))).not.toThrow();
  });

  test('throws for unknown provider', () => {
    const cfg = makeConfig('anthropic');
    (cfg as any).aiProvider = 'unknown-provider';
    expect(() => createReviewer(cfg)).toThrow('Unknown AI provider');
  });

  test('uses ApiDiffFetcher when crossRepo is enabled', () => {
    const cfg = { ...makeConfig('anthropic'), crossRepo: { enabled: true } };
    expect(() => createReviewer(cfg)).not.toThrow();
  });
});
