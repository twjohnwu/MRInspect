import type { Config, MergeRequest } from '../types';
import type { IAIProvider } from '../interfaces/IAIProvider';
import type { IDiffFetcher } from '../interfaces/IDiffFetcher';
import type { IGitLabClient } from '../interfaces/IGitLabClient';
import type { IProjectLoader } from '../interfaces/IProjectLoader';
import type { IPromptComposer } from '../interfaces/IPromptComposer';
import type { IReviewValidator } from '../interfaces/IReviewValidator';
import { ErrorHandler } from '../error/ErrorHandler';
import { selectTemplate } from '../prompt/LegacyTemplates';

export class MRReviewer {
  constructor(
    private readonly gitlab:        IGitLabClient,
    private readonly diffFetcher:   IDiffFetcher,
    private readonly profileLoader: IProjectLoader,
    private readonly promptComposer: IPromptComposer,
    private readonly aiProvider:    IAIProvider,
    private readonly validator:     IReviewValidator,
    private readonly errorHandler:  ErrorHandler,
    private readonly config:        Config,
  ) {}

  async run(): Promise<void> {
    let stage = 'validateSystem';
    try {
      this.validator.validateEnvironment();
      const healthy = await this.gitlab.healthCheck();
      if (!healthy) throw new Error('GitLab API is not reachable');

      stage = 'fetchMRDetails';
      const projectId = this.validator.getProjectId();
      const mrIid     = this.validator.getMrIid();
      const mr        = await this.gitlab.getMergeRequest(projectId, mrIid);
      this.validator.validateMergeRequest(mr);

      stage = 'fetchDiff';
      const diff = await this.fetchDiff(mr);
      this.validator.validateDiff(diff);

      stage = 'generateReview';
      let review = await this.generateReview(diff, mr);

      if (this.config.selfReflection) {
        review = await this.selfReflect(review);
      }

      stage = 'postReview';
      await this.gitlab.postNote(projectId, mrIid, this.validator.sanitize(review));

      console.log(`[mrinspect] review posted for MR !${mrIid} in project ${projectId}`);
    } catch (err) {
      console.error(`[mrinspect] error during ${stage}:`, err);
      await this.postErrorComment(err, stage);
    }
  }

  private async fetchDiff(mr: MergeRequest): Promise<string> {
    if (this.config.crossRepo.enabled) {
      return this.diffFetcher.fetch(mr.sourceBranch, mr.targetBranch);
    }
    const src = this.validator.getSourceBranch();
    const tgt = this.validator.getTargetBranch();
    try {
      return await this.diffFetcher.fetch(src, tgt);
    } catch (err) {
      console.warn('[mrinspect] local diff failed, falling back to API diff:', err);
      return this.diffFetcher.fetch(mr.sourceBranch, mr.targetBranch);
    }
  }

  private async generateReview(diff: string, mr: MergeRequest): Promise<string> {
    let prompt: string;
    try {
      if (!this.profileLoader.isAvailable()) throw new Error('projects directory not available');
      const profile = await this.profileLoader.load(this.config.service.name, this.config.service.type);
      prompt = this.promptComposer.composeReview(profile, diff, mr, profile.resolvedServiceType);
    } catch (err) {
      console.warn('[mrinspect] profile unavailable, using legacy template:', err);
      const tmplFn = selectTemplate(this.config.service.type);
      prompt = tmplFn(diff, mr, this.config.service, this.config.aiProvider);
    }

    const opts = this.config.providers[this.config.aiProvider];
    let lastErr: unknown;

    for (let attempt = 1; attempt <= this.config.validation.aiRetryAttempts; attempt++) {
      if (attempt > 1) {
        prompt += `\n\n---\nPrevious attempt was rejected: ${String(lastErr)}\nEnsure your response includes ## Findings and ## Verdict sections.\n`;
        console.warn(`[mrinspect] retrying AI call (attempt ${attempt})`);
      }

      try {
        const raw = await this.aiProvider.generate(prompt, opts);
        const cleaned = this.cleanResponse(raw);
        this.validator.validateContent(cleaned);
        return cleaned;
      } catch (err) {
        lastErr = err;
      }
    }

    throw new Error(`generateReview: all ${this.config.validation.aiRetryAttempts} attempts failed: ${String(lastErr)}`);
  }

  private async selfReflect(review: string): Promise<string> {
    try {
      if (!this.profileLoader.isAvailable()) return review;
      const profile = await this.profileLoader.load(this.config.service.name, this.config.service.type);
      const reflectPrompt = this.promptComposer.composeSelfReflection(profile, review);
      const result = await this.aiProvider.generate(reflectPrompt, this.config.providers[this.config.aiProvider]);
      if (result.includes('REVIEW VALIDATED')) {
        console.log('[mrinspect] self-reflection: review validated');
        return review;
      }
      const cleaned = this.cleanResponse(result);
      this.validator.validateContent(cleaned);
      console.log('[mrinspect] self-reflection: review updated');
      return cleaned;
    } catch (err) {
      console.warn('[mrinspect] self-reflection failed, using original review:', err);
      return review;
    }
  }

  private async postErrorComment(err: unknown, stage: string): Promise<void> {
    if (!this.errorHandler.shouldPost(err)) return;
    const category = this.errorHandler.categorize(err);
    const message  = this.errorHandler.generateMessage(err, stage, category);
    try {
      await this.gitlab.postNote(
        this.validator.getProjectId(),
        this.validator.getMrIid(),
        message,
      );
    } catch (postErr) {
      console.warn('[mrinspect] failed to post error comment:', postErr);
    }
  }

  private cleanResponse(response: string): string {
    const markers = ['## Code Review', '# Code Review', '## Review', '### MR Info'];
    // Cut at the EARLIEST marker occurrence across the whole list, not the
    // first marker in list-priority order: if the reviewed diff itself
    // quotes a higher-priority marker string near the tail, list-order
    // selection would cut there and discard the entire real review, which
    // is unrecoverable. An echoed marker BEFORE the real content still
    // leaves echo garbage in the result, but that is accepted here because
    // the validator still gates on required sections downstream; a
    // stricter future guard could require the cut tail to actually
    // contain those required sections.
    let cutAt = -1;
    for (const marker of markers) {
      const idx = response.indexOf(marker);
      if (idx >= 0 && (cutAt === -1 || idx < cutAt)) cutAt = idx;
    }
    return cutAt >= 0 ? response.slice(cutAt) : response;
  }
}
