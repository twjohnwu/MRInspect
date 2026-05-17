import type { DiffValidationResult, MergeRequest } from '../types';

export interface IReviewValidator {
  validateEnvironment(): void;
  validateMergeRequest(mr: MergeRequest): void;
  validateDiff(diff: string): DiffValidationResult;
  validateContent(content: string): void;
  sanitize(content: string): string;
  isCrossRepoMode(): boolean;
  getProjectId(): string;
  getMrIid(): string;
  getSourceBranch(): string;
  getTargetBranch(): string;
}
