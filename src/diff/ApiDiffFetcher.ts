import type { Change } from '../types';
import type { IDiffFetcher } from '../interfaces/IDiffFetcher';
import type { IGitLabClient } from '../interfaces/IGitLabClient';

export class ApiDiffFetcher implements IDiffFetcher {
  constructor(private readonly gitlab: IGitLabClient) {}

  async fetch(sourceBranch: string, targetBranch: string): Promise<string> {
    const projectId = process.env.CI_PROJECT_ID ?? process.env.TARGET_PROJECT_ID ?? '';
    const mrIid     = process.env.CI_MERGE_REQUEST_IID ?? process.env.TARGET_MR_IID ?? '';
    const changes   = await this.gitlab.getChanges(projectId, mrIid);
    return convertChangesToDiff(changes);
  }
}

export function convertChangesToDiff(changes: Change[]): string {
  if (changes.length === 0) return '';

  const parts: string[] = [];
  for (const c of changes) {
    const oldPath = c.oldPath || c.newPath;
    const newPath = c.newPath;

    let header: string;
    if (c.newFile) {
      header = `--- /dev/null\n+++ b/${newPath}`;
    } else if (c.deletedFile) {
      header = `--- a/${oldPath}\n+++ /dev/null`;
    } else if (c.renamedFile) {
      header = `--- a/${oldPath}\n+++ b/${newPath}`;
    } else {
      header = `--- a/${oldPath}\n+++ b/${newPath}`;
    }

    const body = c.diff ? (c.diff.endsWith('\n') ? c.diff : c.diff + '\n') : '';
    parts.push(header + '\n' + body);
  }
  return parts.join('');
}
