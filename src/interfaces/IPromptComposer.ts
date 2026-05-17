import type { LoadedProject, MergeRequest } from '../types';

export interface IPromptComposer {
  composeReview(
    profile: LoadedProject,
    diff: string,
    mr: MergeRequest,
    serviceType: string,
  ): string;
  composeSelfReflection(profile: LoadedProject, review: string): string;
}
