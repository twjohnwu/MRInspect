import axios from 'axios';
import type { GenerateOptions, ProviderConfig } from '../types';
import type { IAIProvider } from '../interfaces/IAIProvider';

const OPENAI_API_URL = 'https://api.openai.com/v1/responses';

interface OpenAIResponse {
  output: Array<{
    content: Array<{ text: string }>;
  }>;
}

export class OpenAIProvider implements IAIProvider {
  readonly name = 'openai';
  private readonly apiKey: string;
  private readonly cfg: ProviderConfig;

  constructor(apiKey: string, cfg: ProviderConfig) {
    this.apiKey = apiKey;
    this.cfg = cfg;
  }

  async generate(prompt: string, opts: GenerateOptions): Promise<string> {
    const model     = opts.model     || this.cfg.model;
    const maxTokens = opts.maxTokens || this.cfg.maxTokens;

    const MAX_ATTEMPTS = 3;
    const BASE_DELAY_MS = 1000;
    const MAX_DELAY_MS = 10000;

    let lastErr: unknown;
    for (let attempt = 0; attempt < MAX_ATTEMPTS; attempt++) {
      if (attempt > 0) {
        const delay = Math.min(BASE_DELAY_MS * Math.pow(2, attempt - 1), MAX_DELAY_MS);
        await sleep(delay);
      }

      try {
        const res = await axios.post<OpenAIResponse>(
          OPENAI_API_URL,
          { model, input: prompt, max_output_tokens: maxTokens },
          { headers: { Authorization: `Bearer ${this.apiKey}`, 'Content-Type': 'application/json' } },
        );

        const output = res.data?.output;
        if (!output?.[0]?.content?.[0]?.text) {
          throw new Error('openai: empty output in response');
        }
        return output[0].content[0].text;
      } catch (err) {
        lastErr = err;
        if (axios.isAxiosError(err) && err.response) {
          const status = err.response.status;
          if (status !== 429 && status < 500) throw err;
        }
      }
    }
    throw new Error(`openai: exceeded retry attempts: ${String(lastErr)}`);
  }
}

function sleep(ms: number): Promise<void> {
  return new Promise(resolve => setTimeout(resolve, ms));
}
