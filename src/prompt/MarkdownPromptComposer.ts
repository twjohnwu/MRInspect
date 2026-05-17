import type { DocFile, LoadedProject, MergeRequest } from '../types';
import type { IPromptComposer } from '../interfaces/IPromptComposer';

export class MarkdownPromptComposer implements IPromptComposer {
  composeReview(
    profile: LoadedProject,
    diff: string,
    mr: MergeRequest,
    serviceType: string,
  ): string {
    const date           = new Date().toISOString().split('T')[0];
    const standardNames  = this.standardNames(profile);
    const systemContext  = this.systemContext(profile);
    const catalog        = this.catalog(profile);
    const sharedDocs     = this.formatDocs(profile.sharedDocContents, 'Coding Standards & Conventions');
    const systemDocs     = this.formatDocs(profile.systemDocContents, 'System-Specific Architecture & Patterns');
    const mrInfo         = this.mrInfo(mr);
    const outputTemplate = this.outputTemplate(mr, profile, date, standardNames);

    return `You are a professional code reviewer for the ${profile.system.name} system. Review this merge request against the team standards loaded below. Every finding MUST reference a specific \`file:line\` and cite which standard it violates.

${systemContext}

## Loaded Standards

${catalog}

${sharedDocs}

${systemDocs}

${mrInfo}

## Code Changes

\`\`\`diff
${diff}
\`\`\`

## Review Output Format

${outputTemplate}

## Instructions

1. Focus on ${profile.resolvedServiceType}-specific concerns
2. Every finding must include: severity, category, standard reference, file:line, why it matters
3. Only flag issues in code that was CHANGED in this MR
4. Critical is reserved for bugs, security issues, and data loss — not style
5. If no issues at a severity level, omit that subsection
6. Positive Observations should be specific with file:line, not generic praise`;
  }

  composeSelfReflection(profile: LoadedProject, review: string): string {
    const allDocs = [...profile.sharedDocContents, ...profile.systemDocContents];
    const docsText = allDocs
      .map(d => `### ${d.filename.replace('.md', '').replace(/-/g, ' ')}\n\n${d.content}`)
      .join('\n\n---\n\n');

    return `You are a code review quality validator for the ${profile.system.name} system. Verify every finding in the review below against the loaded standards.

## Validation Checklist

For each finding verify:
1. Does the cited Standard contain the rule being referenced?
2. Is the Severity appropriate? (Critical = bugs/security/data loss ONLY)
3. Does the finding reference rules from other systems that don't apply here?
4. Does each finding have a concrete "Why it matters" — not vague?

## System Standards

${docsText}

## Original Review

${review}

## Output Instructions

Correct any findings that cite wrong standards, wrong categories, or inappropriate severity. Maintain the exact markdown structure. If all findings are valid, output the original review unchanged. If valid, include "REVIEW VALIDATED" at the end.`;
  }

  private systemContext(profile: LoadedProject): string {
    return [
      `## System: ${profile.system.name}`,
      profile.system.description,
      '',
      `### Frameworks & Technologies`,
      profile.system.frameworks.join(', '),
    ].join('\n');
  }

  private catalog(profile: LoadedProject): string {
    const lines = [
      ...profile.sharedDocContents.map(d => `- \`${d.filename.replace('.md', '')}\` (shared)`),
      ...profile.systemDocContents.map(d => `- \`${d.filename.replace('.md', '')}\` (${profile.system.name})`),
    ];
    return lines.join('\n');
  }

  private formatDocs(docs: DocFile[], title: string): string {
    if (docs.length === 0) return '';
    const sections = docs.map(d => {
      const heading = d.filename.replace('.md', '').replace(/-/g, ' ');
      return `### ${heading}\n\n${d.content}`;
    });
    return `## ${title}\n\n${sections.join('\n\n---\n\n')}`;
  }

  private mrInfo(mr: MergeRequest): string {
    return [
      '## Merge Request',
      '',
      `- **MR**: !${mr.iid} — ${mr.title}`,
      `- **Author**: ${mr.author?.name ?? 'Unknown'}`,
      `- **Branch**: ${mr.sourceBranch} → ${mr.targetBranch}`,
      `- **Description**: ${mr.description || 'No description provided'}`,
    ].join('\n');
  }

  private standardNames(profile: LoadedProject): string {
    return [
      ...profile.sharedDocContents.map(d => d.filename.replace('.md', '')),
      ...profile.systemDocContents.map(d => d.filename.replace('.md', '')),
    ].join(', ');
  }

  private outputTemplate(mr: MergeRequest, profile: LoadedProject, date: string, standards: string): string {
    return `## Code Review: MR !${mr.iid}

### MR Info
- **Title**: ${mr.title}
- **Author**: ${mr.author?.name ?? 'Unknown'}
- **Branch**: ${mr.sourceBranch} → ${mr.targetBranch}
- **System**: ${profile.system.name}
- **Date**: ${date}
- **Standards Referenced**: ${standards}

### Findings

| # | Severity | Category | Standard | Item | File:Line |
|---|----------|----------|----------|------|-----------|

### Details

#### Critical
[For each critical finding: **#N — \`file:line\`** — description, standard violated, why it matters, suggestion]

#### Warning
[For each warning: **#N — \`file:line\`** — description, standard violated, why it matters, suggestion]

#### Nit
[For each nit: **#N — \`file:line\`** — brief reason]

### Positive Observations
- [Specific file:line reference for each]

### Verdict
[LGTM / Needs Changes / Needs Minor Changes]

**Reasoning**: [1-2 sentence technical assessment]`;
  }
}
