import type { Config, DiffValidationResult, MergeRequest, ValidationConfig } from '../types';
import { ValidationError } from '../types';
import type { IReviewValidator } from '../interfaces/IReviewValidator';

const UNSAFE = /(?:javascript:|on\w+=|<script|<\/script)/gi;

export class ReviewContentValidator implements IReviewValidator {
  private readonly cfg: ValidationConfig;

  constructor(cfg: ValidationConfig) {
    this.cfg = cfg;
  }

  isCrossRepoMode(): boolean {
    return process.env.CI_PIPELINE_SOURCE === 'trigger';
  }

  getProjectId(): string {
    return this.isCrossRepoMode()
      ? (process.env.MRI_PROJECT_ID ?? '')
      : (process.env.CI_PROJECT_ID ?? '');
  }

  getMrIid(): string {
    return this.isCrossRepoMode()
      ? (process.env.MRI_MR_IID ?? '')
      : (process.env.CI_MERGE_REQUEST_IID ?? '');
  }

  getSourceBranch(): string {
    return this.isCrossRepoMode()
      ? (process.env.MRI_SOURCE_BRANCH ?? '')
      : (process.env.CI_COMMIT_REF_NAME ?? '');
  }

  getTargetBranch(): string {
    return this.isCrossRepoMode()
      ? (process.env.MRI_TARGET_BRANCH ?? '')
      : (process.env.CI_MERGE_REQUEST_TARGET_BRANCH_NAME ?? '');
  }

  validateEnvironment(): void {
    const required = ['AI_PROVIDER_KEY', 'GITLAB_TOKEN'];
    if (this.isCrossRepoMode()) {
      required.push('MRI_PROJECT_ID', 'MRI_MR_IID', 'MRI_SOURCE_BRANCH', 'MRI_TARGET_BRANCH');
    } else {
      required.push('CI_PROJECT_ID', 'CI_MERGE_REQUEST_IID');
    }
    const missing = required.filter(k => !process.env[k]);
    if (missing.length > 0) {
      throw new ValidationError(
        'MISSING_ENV_VARS',
        `missing required environment variables: ${missing.join(', ')}`,
      );
    }
  }

  validateMergeRequest(mr: MergeRequest): void {
    if (!mr.iid) throw new ValidationError('INVALID_MR', 'merge request IID is missing');
    if (!mr.title) throw new ValidationError('INVALID_MR', 'merge request title is empty');
  }

  validateDiff(diff: string): DiffValidationResult {
    const sizeKB = Buffer.byteLength(diff, 'utf-8') / 1024;
    const result: DiffValidationResult = { sizeKB, filesChanged: 0, supportedFiles: 0 };

    if (!diff) throw new ValidationError('EMPTY_DIFF', 'diff is empty — no changes to review');

    if (sizeKB > this.cfg.maxDiffSizeKB) {
      throw new ValidationError(
        'DIFF_TOO_LARGE',
        `diff size ${sizeKB.toFixed(1)} KB exceeds limit ${this.cfg.maxDiffSizeKB} KB`,
      );
    }

    for (const line of diff.split('\n')) {
      if (line.startsWith('+++ b/') || line.startsWith('+++ /dev/null')) {
        result.filesChanged++;
        const filename = line.replace('+++ b/', '');
        if (this.isSupportedExt(filename)) result.supportedFiles++;
      }
    }

    if (result.filesChanged > this.cfg.maxFilesChanged) {
      throw new ValidationError(
        'TOO_MANY_FILES',
        `diff contains ${result.filesChanged} files, exceeds limit of ${this.cfg.maxFilesChanged}`,
      );
    }

    if (result.filesChanged > 0 && result.supportedFiles === 0) {
      throw new ValidationError('NO_SUPPORTED_FILES', 'diff contains no files with supported extensions');
    }

    return result;
  }

  validateContent(content: string): void {
    if (content.length < 100) {
      throw new ValidationError('REVIEW_TOO_SHORT', 'review content is too short (< 100 chars)');
    }
    for (const section of ['##', 'Findings', 'Verdict']) {
      if (!content.includes(section)) {
        throw new ValidationError('MISSING_SECTION', `review is missing required section: "${section}"`);
      }
    }
  }

  sanitize(content: string): string {
    return UNSAFE.exec(content)
      ? content.replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(UNSAFE, '')
      : content.replace(/</g, '&lt;').replace(/>/g, '&gt;');
  }

  private isSupportedExt(filename: string): boolean {
    const lower = filename.toLowerCase();
    return this.cfg.allowedExtensions.some(ext => lower.endsWith(ext));
  }
}
