export interface IDiffFetcher {
  fetch(sourceBranch: string, targetBranch: string): Promise<string>;
}
