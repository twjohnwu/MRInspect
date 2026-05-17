import { ValidationError } from '../types';
import type { ErrorReportingConfig } from '../types';

export type ErrorCategory = 'configuration' | 'api' | 'validation' | 'system' | 'unknown';

export class ErrorHandler {
  constructor(private readonly cfg: ErrorReportingConfig) {}

  categorize(err: unknown): ErrorCategory {
    if (err instanceof ValidationError) return 'validation';
    const msg = String(err).toLowerCase();
    if (msg.includes('ai_provider_key') || msg.includes('gitlab_token') ||
        msg.includes('configuration') || msg.includes('missing required')) {
      return 'configuration';
    }
    if (msg.includes('timeout') || msg.includes('rate limit') ||
        msg.includes('429') || msg.includes('503') ||
        msg.includes('api error') || msg.includes('econnrefused')) {
      return 'api';
    }
    if (msg.includes('no such file') || msg.includes('permission denied') ||
        msg.includes('git') || msg.includes('repository')) {
      return 'system';
    }
    return 'unknown';
  }

  shouldPost(err: unknown): boolean {
    if (!this.cfg.postToMR) return false;
    const msg = String(err).toLowerCase();
    if (msg.includes('429') || msg.includes('rate limit')) return false;
    return true;
  }

  generateMessage(err: unknown, stage: string, category: ErrorCategory): string {
    const friendly = this.friendlyStage(stage);
    const errMsg   = truncate(String(err), this.cfg.maxMessageLength);
    const steps    = this.troubleshootingSteps(category);

    const lines = [
      '## MRInspect: Review Failed',
      '',
      `An error occurred during **${friendly}**.`,
      '',
      `**Error**: \`${errMsg}\``,
      '',
      `**Category**: ${category}`,
    ];

    if (steps.length > 0) {
      lines.push('', '**Troubleshooting**:');
      for (const s of steps) lines.push(`- ${s}`);
    }

    return lines.join('\n');
  }

  private friendlyStage(stage: string): string {
    const map: Record<string, string> = {
      validateSystem:  'system validation',
      fetchMRDetails:  'fetching MR details',
      fetchDiff:       'fetching code changes',
      generateReview:  'generating AI review',
      selfReflect:     'self-reflection validation',
      postReview:      'posting review comment',
    };
    return map[stage] ?? stage;
  }

  private troubleshootingSteps(category: ErrorCategory): string[] {
    switch (category) {
      case 'configuration':
        return [
          'Verify GITLAB_TOKEN and AI_PROVIDER_KEY are set in CI/CD variables',
          'Check that the token has API access to the target project',
        ];
      case 'api':
        return [
          'Check the AI provider status page for outages',
          'Verify the GitLab API is reachable from the CI runner',
          'The job will succeed automatically on next pipeline run',
        ];
      case 'validation':
        return [
          'Ensure CI_PROJECT_ID and CI_MERGE_REQUEST_IID are set',
          'Check that the diff is within the 300 KB size limit',
        ];
      case 'system':
        return [
          'Verify the CI runner has git access to the repository',
          'Check file system permissions on the projects directory',
        ];
      default:
        return [
          'Check the job logs for more details',
          'Contact the platform team if the issue persists',
        ];
    }
  }
}

function truncate(s: string, max: number): string {
  if (max <= 0 || s.length <= max) return s;
  return s.slice(0, max) + '...';
}
