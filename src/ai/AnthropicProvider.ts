import Anthropic from '@anthropic-ai/sdk';
import type { GenerateOptions, ProviderConfig } from '../types';
import type { IAIProvider } from '../interfaces/IAIProvider';

export class AnthropicProvider implements IAIProvider {
  readonly name = 'anthropic';
  private readonly client: Anthropic;
  private readonly cfg: ProviderConfig;

  constructor(apiKey: string, cfg: ProviderConfig) {
    this.client = new Anthropic({ apiKey });
    this.cfg = cfg;
  }

  async generate(prompt: string, opts: GenerateOptions): Promise<string> {
    const model     = opts.model     || this.cfg.model;
    const maxTokens = opts.maxTokens || this.cfg.maxTokens;

    const msg = await this.client.messages.create({
      model,
      max_tokens: maxTokens,
      messages: [{ role: 'user', content: prompt }],
    });

    const block = msg.content[0];
    if (!block || block.type !== 'text') {
      throw new Error('anthropic: empty or non-text response');
    }
    return block.text;
  }
}
