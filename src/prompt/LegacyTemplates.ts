import type { AIProviderName, MergeRequest } from '../types';

interface ServiceInfo { name: string; type: string }

type TemplateFn = (diff: string, mr: MergeRequest, svc: ServiceInfo, provider: AIProviderName) => string;

function baseInstructions(diff: string, mr: MergeRequest, svc: ServiceInfo, focusAreas: string): string {
  return `You are a professional code reviewer. Review this merge request thoroughly.

## Merge Request
- **MR**: !${mr.iid} — ${mr.title}
- **Author**: ${mr.author?.name ?? 'Unknown'}
- **Branch**: ${mr.sourceBranch} → ${mr.targetBranch}
- **Service**: ${svc.name} (${svc.type})

## Focus Areas
${focusAreas}

## Code Changes

\`\`\`diff
${diff}
\`\`\`

## Output Format

Provide a structured review with:
1. **Findings** table — severity (Critical/Warning/Nit), category, file:line, description
2. **Details** — one section per severity with specific fix suggestions
3. **Verdict** — LGTM / Needs Changes / Needs Minor Changes, with reasoning`;
}

export function backendTemplate(diff: string, mr: MergeRequest, svc: ServiceInfo, provider: AIProviderName): string {
  return baseInstructions(diff, mr, svc, `
- API design (routes, DTOs, HTTP methods, status codes)
- Database queries (N+1, indexes, transactions)
- Error handling (typed errors, proper propagation)
- Security (input validation, auth guards, injection)
- TypeScript/NestJS patterns (DI, decorators, lifecycle)
- Test coverage (unit + integration)`);
}

export function frontendTemplate(diff: string, mr: MergeRequest, svc: ServiceInfo, provider: AIProviderName): string {
  return baseInstructions(diff, mr, svc, `
- React component design (hooks, props, memoization)
- Accessibility (ARIA, keyboard nav, semantic HTML)
- Performance (bundle size, lazy loading, re-renders)
- Error boundaries and loading states
- TypeScript type safety
- CSS/Tailwind conventions`);
}

export function aiServiceTemplate(diff: string, mr: MergeRequest, svc: ServiceInfo, provider: AIProviderName): string {
  return baseInstructions(diff, mr, svc, `
- Prompt design and token efficiency
- Model selection and fallback strategy
- Error handling for AI provider failures
- Cost and latency considerations
- Input/output validation for AI calls
- Retry and timeout logic`);
}

export function terraformTemplate(diff: string, mr: MergeRequest, svc: ServiceInfo, provider: AIProviderName): string {
  return baseInstructions(diff, mr, svc, `
- Resource naming conventions
- IAM least-privilege principle
- Security group and network rules
- State management and remote backends
- Sensitive data handling (no hardcoded secrets)
- Module structure and reuse`);
}

export function comprehensiveTemplate(diff: string, mr: MergeRequest, svc: ServiceInfo, provider: AIProviderName): string {
  return baseInstructions(diff, mr, svc, `
- Code correctness and logic errors
- Security vulnerabilities (OWASP Top 10)
- Performance and scalability concerns
- Error handling and edge cases
- Code quality and maintainability
- Test coverage`);
}

export function selectTemplate(serviceType: string): TemplateFn {
  switch (serviceType) {
    case 'frontend': return frontendTemplate;
    case 'ai':       return aiServiceTemplate;
    case 'iac':
    case 'terraform': return terraformTemplate;
    case 'backend':  return backendTemplate;
    default:         return comprehensiveTemplate;
  }
}
