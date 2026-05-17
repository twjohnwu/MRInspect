import { loadConfig } from '../src/config/ReviewConfig';

const REQUIRED = {
  AI_PROVIDER_KEY: 'test-key',
  GITLAB_TOKEN:    'test-token',
  CI_PROJECT_ID:   '1',
  CI_MERGE_REQUEST_IID: '2',
};

function withEnv(vars: Record<string, string>, fn: () => void): void {
  const saved: Record<string, string | undefined> = {};
  for (const [k, v] of Object.entries(vars)) {
    saved[k] = process.env[k];
    process.env[k] = v;
  }
  try {
    fn();
  } finally {
    for (const [k, v] of Object.entries(saved)) {
      if (v === undefined) delete process.env[k];
      else process.env[k] = v;
    }
  }
}

beforeEach(() => {
  // Clear all relevant env vars before each test
  const keys = [
    'AI_PROVIDER_KEY', 'GITLAB_TOKEN', 'AI_PROVIDER',
    'CI_PROJECT_ID', 'CI_MERGE_REQUEST_IID', 'CI_PIPELINE_SOURCE',
    'TARGET_PROJECT_ID', 'TARGET_MR_IID', 'SOURCE_BRANCH', 'TARGET_BRANCH',
    'IS_SELF_REFLECTION', 'PROJECTS_DIR', 'ANTHROPIC_MODEL',
    'GEMINI_MODEL', 'OPENAI_MODEL', 'ANTHROPIC_MAX_TOKENS',
  ];
  for (const k of keys) delete process.env[k];
});

describe('loadConfig', () => {
  test('throws when AI_PROVIDER_KEY is missing', () => {
    expect(() => loadConfig()).toThrow('AI_PROVIDER_KEY');
  });

  test('throws when GITLAB_TOKEN is missing', () => {
    process.env.AI_PROVIDER_KEY = 'k';
    expect(() => loadConfig()).toThrow('GITLAB_TOKEN');
  });

  test('throws for unknown AI_PROVIDER', () => {
    withEnv({ ...REQUIRED, AI_PROVIDER: 'unknown' }, () => {
      expect(() => loadConfig()).toThrow('Unsupported AI_PROVIDER');
    });
  });

  test('defaults to gemini provider', () => {
    withEnv(REQUIRED, () => {
      const cfg = loadConfig();
      expect(cfg.aiProvider).toBe('gemini');
    });
  });

  test('accepts all three providers', () => {
    for (const provider of ['anthropic', 'gemini', 'openai'] as const) {
      withEnv({ ...REQUIRED, AI_PROVIDER: provider }, () => {
        const cfg = loadConfig();
        expect(cfg.aiProvider).toBe(provider);
      });
    }
  });

  test('self-reflection defaults to false', () => {
    withEnv(REQUIRED, () => {
      expect(loadConfig().selfReflection).toBe(false);
    });
  });

  test('self-reflection enabled when IS_SELF_REFLECTION=true', () => {
    withEnv({ ...REQUIRED, IS_SELF_REFLECTION: 'true' }, () => {
      expect(loadConfig().selfReflection).toBe(true);
    });
  });

  test('default model values are correct', () => {
    withEnv(REQUIRED, () => {
      const cfg = loadConfig();
      expect(cfg.providers.anthropic.model).toBe('claude-3-5-sonnet-20241022');
      expect(cfg.providers.gemini.model).toBe('gemini-2.5-pro');
      expect(cfg.providers.openai.model).toBe('gpt-5');
    });
  });

  test('PROJECTS_DIR defaults to ../projects', () => {
    withEnv(REQUIRED, () => {
      expect(loadConfig().projects.directory).toBe('./projects');
    });
  });

  test('PROJECTS_DIR is overridden from env', () => {
    withEnv({ ...REQUIRED, PROJECTS_DIR: '/custom/projects' }, () => {
      expect(loadConfig().projects.directory).toBe('/custom/projects');
    });
  });
});
