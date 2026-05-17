import { GoogleGenerativeAI } from '@google/generative-ai';
import type { GenerateOptions, ProviderConfig } from '../types';
import type { IAIProvider } from '../interfaces/IAIProvider';

export class GeminiProvider implements IAIProvider {
  readonly name = 'gemini';
  private readonly client: GoogleGenerativeAI;
  private readonly cfg: ProviderConfig;

  constructor(apiKey: string, cfg: ProviderConfig) {
    this.client = new GoogleGenerativeAI(apiKey);
    this.cfg = cfg;
  }

  async generate(prompt: string, opts: GenerateOptions): Promise<string> {
    const model     = opts.model     || this.cfg.model;
    const maxTokens = opts.maxTokens || this.cfg.maxTokens;

    const genModel = this.client.getGenerativeModel({
      model,
      generationConfig: { maxOutputTokens: maxTokens, temperature: this.cfg.temperature },
    });

    const result = await genModel.generateContent(prompt);
    const text = result.response.text();
    if (!text) throw new Error('gemini: empty response');
    return text;
  }
}
