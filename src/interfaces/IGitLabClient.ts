import type { Change, MergeRequest } from '../types';

export interface IGitLabClient {
  healthCheck(): Promise<boolean>;
  getMergeRequest(projectId: string, mrIid: string): Promise<MergeRequest>;
  getChanges(projectId: string, mrIid: string): Promise<Change[]>;
  postNote(projectId: string, mrIid: string, body: string): Promise<void>;
}
