// ── GitLab data shapes ────────────────────────────────────────────────────────

export interface MergeRequest {
  id: number;
  iid: number;
  title: string;
  description: string;
  sourceBranch: string;
  targetBranch: string;
  author: { id: number; name: string; username: string };
  changesCount: string;
  webUrl: string;
}

export interface Change {
  oldPath: string;
  newPath: string;
  diff: string;
  newFile: boolean;
  renamedFile: boolean;
  deletedFile: boolean;
}

export interface MRChangesResponse {
  changes: Change[];
}

// ── Project shapes ────────────────────────────────────────────────────────────

export interface SystemProject {
  name: string;
  description: string;
  defaultServiceType: string;
  frameworks: string[];
  serviceTypeOverrides?: Record<string, string>;
}

export interface ServiceRegistry {
  defaultSystem: string;
  services: Record<string, string>;
}

export interface DocFile {
  filename: string;
  content: string;
}

export interface LoadedProject {
  system: SystemProject;
  sharedDocContents: DocFile[];
  systemDocContents: DocFile[];
  resolvedServiceType: string;
}

// ── AI shapes ─────────────────────────────────────────────────────────────────

export type AIProviderName = 'anthropic' | 'gemini' | 'openai';

export interface GenerateOptions {
  model: string;
  maxTokens: number;
  temperature?: number;
}

// ── Config shape ──────────────────────────────────────────────────────────────

export interface ProviderConfig {
  model: string;
  maxTokens: number;
  temperature: number;
}

export interface GitLabConfig {
  apiBase: string;
  token: string;
  timeoutMs: number;
  retryAttempts: number;
  retryDelayMs: number;
  maxRetryDelayMs: number;
}

export interface ProjectsConfig {
  directory: string;
  registryFile: string;
  sharedDir: string;
}

export interface ValidationConfig {
  allowedExtensions: string[];
  maxFilesChanged: number;
  maxDiffSizeKB: number;
  aiRetryAttempts: number;
}

export interface ErrorReportingConfig {
  postToMR: boolean;
  maxMessageLength: number;
}

export interface Config {
  aiProvider: AIProviderName;
  aiProviderKey: string;
  selfReflection: boolean;
  service: { name: string; type: string };
  crossRepo: { enabled: boolean };
  gitlab: GitLabConfig;
  providers: Record<AIProviderName, ProviderConfig>;
  projects: ProjectsConfig;
  validation: ValidationConfig;
  errorReporting: ErrorReportingConfig;
  metricsFile: string;
  logLevel: string;
}

// ── Validation error ──────────────────────────────────────────────────────────

export class ValidationError extends Error {
  constructor(
    public readonly code: string,
    message: string,
  ) {
    super(message);
    this.name = 'ValidationError';
  }
}

export interface DiffValidationResult {
  sizeKB: number;
  filesChanged: number;
  supportedFiles: number;
}
