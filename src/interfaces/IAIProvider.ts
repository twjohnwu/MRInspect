import type { GenerateOptions } from '../types';

export interface IAIProvider {
  readonly name: string;
  generate(prompt: string, opts: GenerateOptions): Promise<string>;
}
