import type { AIProviderName, Config } from '../types';

function env(key: string, fallback: string): string {
  return process.env[key] || fallback;
}

function envInt(key: string, fallback: number): number {
  const v = process.env[key];
  if (v) {
    const n = parseInt(v, 10);
    if (!isNaN(n)) return n;
  }
  return fallback;
}

const VALID_PROVIDERS: AIProviderName[] = ['anthropic', 'gemini', 'openai'];

export function loadConfig(): Config {
  const aiProviderKey = process.env.AI_PROVIDER_KEY;
  if (!aiProviderKey) throw new Error('AI_PROVIDER_KEY environment variable is required');

  const gitlabToken = process.env.GITLAB_TOKEN;
  if (!gitlabToken) throw new Error('GITLAB_TOKEN environment variable is required');

  const rawProvider = env('AI_PROVIDER', 'openai').toLowerCase() as AIProviderName;
  if (!VALID_PROVIDERS.includes(rawProvider)) {
    throw new Error(`Unsupported AI_PROVIDER "${rawProvider}": must be one of [${VALID_PROVIDERS.join(', ')}]`);
  }

  const projectsDir = env('PROJECTS_DIR', './projects');

  return {
    aiProvider: rawProvider,
    aiProviderKey,
    selfReflection: env('IS_SELF_REFLECTION', 'false') === 'true',

    service: {
      name: env('MRI_SERVICE_NAME', 'unknown'),
      type: env('MRI_SERVICE_TYPE', 'backend'),
    },

    crossRepo: {
      enabled: process.env.CI_PIPELINE_SOURCE === 'trigger',
    },

    gitlab: {
      apiBase:         env('GITLAB_API_BASE', 'https://gitlab.com/api/v4'),
      token:           gitlabToken,
      timeoutMs:       envInt('API_TIMEOUT_MS', 30000),
      retryAttempts:   envInt('API_RETRY_ATTEMPTS', 3),
      retryDelayMs:    envInt('API_RETRY_DELAY_MS', 1000),
      maxRetryDelayMs: envInt('API_MAX_RETRY_DELAY_MS', 10000),
    },

    providers: {
      anthropic: {
        model:       env('ANTHROPIC_MODEL', 'claude-sonnet-5'),
        maxTokens:   envInt('ANTHROPIC_MAX_TOKENS', 4000),
        temperature: 0.1,
      },
      gemini: {
        model:       env('GEMINI_MODEL', 'gemini-2.5-pro'),
        maxTokens:   envInt('GEMINI_MAX_TOKENS', 8000),
        temperature: 0.1,
      },
      openai: {
        model:       env('OPENAI_MODEL', 'gpt-5.6'),
        maxTokens:   envInt('OPENAI_MAX_TOKENS', 4000),
        temperature: 0.1,
      },
    },

    projects: {
      directory:    projectsDir,
      registryFile: `${projectsDir}/registry.yaml`,
      sharedDir:    `${projectsDir}/_shared`,
    },

    validation: {
      allowedExtensions: [
        '.ts', '.js', '.tsx', '.jsx', '.py', '.go',
        '.tf', '.hcl', '.json', '.yaml', '.yml', '.md',
        '.sql', '.sh', '.bash', '.env.example',
      ],
      maxFilesChanged: envInt('MAX_FILES_CHANGED', 50),
      maxDiffSizeKB:   300,
      aiRetryAttempts: envInt('AI_RETRY_ATTEMPTS', 3),
    },

    errorReporting: {
      postToMR:         true,
      maxMessageLength: 2000,
    },

    metricsFile: env('AI_REVIEW_METRICS_FILE', './mrinspect-metrics.json'),
    logLevel:    env('LOG_LEVEL', 'info'),
  };
}
