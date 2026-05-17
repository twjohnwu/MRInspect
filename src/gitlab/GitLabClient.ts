import axios, { type AxiosInstance } from 'axios';
import type { Change, GitLabConfig, MergeRequest, MRChangesResponse } from '../types';
import type { IGitLabClient } from '../interfaces/IGitLabClient';

export class GitLabClient implements IGitLabClient {
  private readonly http: AxiosInstance;
  private readonly token: string;
  private readonly retry: { attempts: number; delayMs: number; maxDelayMs: number };

  constructor(cfg: GitLabConfig) {
    this.token = cfg.token;
    this.retry = {
      attempts:   cfg.retryAttempts,
      delayMs:    cfg.retryDelayMs,
      maxDelayMs: cfg.maxRetryDelayMs,
    };
    this.http = axios.create({
      baseURL: cfg.apiBase,
      timeout: cfg.timeoutMs,
      headers: { 'PRIVATE-TOKEN': cfg.token, 'Content-Type': 'application/json' },
    });
  }

  async healthCheck(): Promise<boolean> {
    try {
      const res = await this.http.get('/version');
      return res.status === 200;
    } catch {
      return false;
    }
  }

  async getMergeRequest(projectId: string, mrIid: string): Promise<MergeRequest> {
    const data = await this.get<{
      id: number; iid: number; title: string; description: string;
      source_branch: string; target_branch: string;
      author: { id: number; name: string; username: string };
      changes_count: string; web_url: string;
    }>(`/projects/${projectId}/merge_requests/${mrIid}`);

    return {
      id:           data.id,
      iid:          data.iid,
      title:        data.title,
      description:  data.description ?? '',
      sourceBranch: data.source_branch,
      targetBranch: data.target_branch,
      author:       data.author,
      changesCount: data.changes_count ?? '0',
      webUrl:       data.web_url,
    };
  }

  async getChanges(projectId: string, mrIid: string): Promise<Change[]> {
    const data = await this.get<{ changes: Array<{
      old_path: string; new_path: string; diff: string;
      new_file: boolean; renamed_file: boolean; deleted_file: boolean;
    }> }>(`/projects/${projectId}/merge_requests/${mrIid}/changes`);

    return (data.changes ?? []).map(c => ({
      oldPath:     c.old_path,
      newPath:     c.new_path,
      diff:        c.diff ?? '',
      newFile:     c.new_file,
      renamedFile: c.renamed_file,
      deletedFile: c.deleted_file,
    }));
  }

  async postNote(projectId: string, mrIid: string, body: string): Promise<void> {
    await this.post(`/projects/${projectId}/merge_requests/${mrIid}/notes`, { body });
  }

  private async get<T>(path: string): Promise<T> {
    return this.withRetry(async () => {
      const res = await this.http.get<T>(path);
      return res.data;
    });
  }

  private async post<T>(path: string, payload: unknown): Promise<T> {
    return this.withRetry(async () => {
      const res = await this.http.post<T>(path, payload);
      return res.data;
    });
  }

  private async withRetry<T>(fn: () => Promise<T>): Promise<T> {
    let lastErr: unknown;
    for (let attempt = 0; attempt <= this.retry.attempts; attempt++) {
      if (attempt > 0) {
        const delay = Math.min(
          this.retry.delayMs * Math.pow(2, attempt - 1),
          this.retry.maxDelayMs,
        );
        await sleep(delay);
      }
      try {
        return await fn();
      } catch (err) {
        lastErr = err;
        if (axios.isAxiosError(err) && err.response && err.response.status < 500) {
          throw err;
        }
      }
    }
    throw lastErr;
  }
}

function sleep(ms: number): Promise<void> {
  return new Promise(resolve => setTimeout(resolve, ms));
}
