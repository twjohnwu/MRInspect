import type { Config } from './types';
import type { IAIProvider } from './interfaces/IAIProvider';
import { AnthropicProvider } from './ai/AnthropicProvider';
import { GeminiProvider } from './ai/GeminiProvider';
import { OpenAIProvider } from './ai/OpenAIProvider';
import { GitLabClient } from './gitlab/GitLabClient';
import { GitDiffFetcher } from './diff/GitDiffFetcher';
import { ApiDiffFetcher } from './diff/ApiDiffFetcher';
import { YamlProjectLoader } from './project/YamlProjectLoader';
import { MarkdownPromptComposer } from './prompt/MarkdownPromptComposer';
import { ReviewContentValidator } from './review/ReviewContentValidator';
import { ErrorHandler } from './error/ErrorHandler';
import { MRReviewer } from './review/MRReviewer';

export function createReviewer(config: Config): MRReviewer {
  const gitlab = new GitLabClient(config.gitlab);

  const diffFetcher = config.crossRepo.enabled
    ? new ApiDiffFetcher(gitlab)
    : new GitDiffFetcher();

  const projectLoader  = new YamlProjectLoader(config.projects.directory);
  const promptComposer = new MarkdownPromptComposer();

  const aiProviders: Record<string, IAIProvider> = {
    anthropic: new AnthropicProvider(config.aiProviderKey, config.providers.anthropic),
    gemini:    new GeminiProvider(config.aiProviderKey, config.providers.gemini),
    openai:    new OpenAIProvider(config.aiProviderKey, config.providers.openai),
  };

  const aiProvider = aiProviders[config.aiProvider];
  if (!aiProvider) throw new Error(`Unknown AI provider: ${config.aiProvider}`);

  const validator    = new ReviewContentValidator(config.validation);
  const errorHandler = new ErrorHandler(config.errorReporting);

  return new MRReviewer(
    gitlab,
    diffFetcher,
    projectLoader,
    promptComposer,
    aiProvider,
    validator,
    errorHandler,
    config,
  );
}
