import simpleGit from 'simple-git';
import type { IDiffFetcher } from '../interfaces/IDiffFetcher';

export class GitDiffFetcher implements IDiffFetcher {
  async fetch(sourceBranch: string, targetBranch: string): Promise<string> {
    const git = simpleGit('.');
    try {
      return await git.diff([`origin/${targetBranch}...origin/${sourceBranch}`]);
    } catch {
      // Fall back to local refs without remote prefix
      return git.diff([`${targetBranch}...${sourceBranch}`]);
    }
  }
}
