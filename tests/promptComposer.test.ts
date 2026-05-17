import { MarkdownPromptComposer } from '../src/prompt/MarkdownPromptComposer';
import type { LoadedProject, MergeRequest } from '../src/types';

function makeMR(overrides: Partial<MergeRequest> = {}): MergeRequest {
  return {
    id:           1,
    iid:          42,
    title:        'Add feature X',
    description:  'Adds new feature X',
    sourceBranch: 'feature/x',
    targetBranch: 'main',
    author:       { id: 1, name: 'Alice', username: 'alice' },
    changesCount: '5',
    webUrl:       'https://example.com/mr/42',
    ...overrides,
  };
}

function makeProject(overrides: Partial<LoadedProject> = {}): LoadedProject {
  return {
    system: {
      name:               'Test System',
      description:        'A test system',
      defaultServiceType: 'backend',
      frameworks:         ['TypeScript', 'Node.js'],
    },
    sharedDocContents: [
      { filename: 'coding-standards.md', content: '# Coding Standards\nRule: use strict mode' },
    ],
    systemDocContents: [
      { filename: 'architecture.md', content: '# Architecture\nUse layered architecture' },
    ],
    resolvedServiceType: 'backend',
    ...overrides,
  };
}

describe('MarkdownPromptComposer.composeReview', () => {
  const composer = new MarkdownPromptComposer();

  test('output contains MR number', () => {
    const prompt = composer.composeReview(makeProject(), 'diff content', makeMR(), 'backend');
    expect(prompt).toContain('!42');
  });

  test('output contains system name', () => {
    const prompt = composer.composeReview(makeProject(), 'diff content', makeMR(), 'backend');
    expect(prompt).toContain('Test System');
  });

  test('output contains diff content', () => {
    const prompt = composer.composeReview(makeProject(), 'the actual diff', makeMR(), 'backend');
    expect(prompt).toContain('the actual diff');
  });

  test('output contains ## Findings in template', () => {
    const prompt = composer.composeReview(makeProject(), 'diff', makeMR(), 'backend');
    expect(prompt).toContain('Findings');
  });

  test('output contains Verdict in template', () => {
    const prompt = composer.composeReview(makeProject(), 'diff', makeMR(), 'backend');
    expect(prompt).toContain('Verdict');
  });

  test('output references shared doc filename', () => {
    const prompt = composer.composeReview(makeProject(), 'diff', makeMR(), 'backend');
    expect(prompt).toContain('coding-standards');
  });

  test('output references system doc content', () => {
    const prompt = composer.composeReview(makeProject(), 'diff', makeMR(), 'backend');
    expect(prompt).toContain('Use layered architecture');
  });

  test('output contains author name', () => {
    const prompt = composer.composeReview(makeProject(), 'diff', makeMR(), 'backend');
    expect(prompt).toContain('Alice');
  });
});

describe('MarkdownPromptComposer.composeSelfReflection', () => {
  const composer = new MarkdownPromptComposer();

  test('output contains REVIEW VALIDATED instruction', () => {
    const prompt = composer.composeSelfReflection(makeProject(), '## Code Review...\n### Findings\n### Verdict');
    expect(prompt).toContain('REVIEW VALIDATED');
  });

  test('output contains the original review', () => {
    const review = '## Code Review\n### Findings\n### Verdict';
    const prompt  = composer.composeSelfReflection(makeProject(), review);
    expect(prompt).toContain(review);
  });

  test('output contains system standards docs', () => {
    const prompt = composer.composeSelfReflection(makeProject(), 'review content');
    expect(prompt).toContain('Use layered architecture');
  });
});
