import { ReviewContentValidator } from '../src/review/ReviewContentValidator';
import { ValidationError } from '../src/types';
import type { ValidationConfig } from '../src/types';

function assertValidationCode(fn: () => unknown, expectedCode: string): void {
  let thrown: unknown;
  try { fn(); } catch (e) { thrown = e; }
  expect(thrown).toBeInstanceOf(ValidationError);
  expect((thrown as ValidationError).code).toBe(expectedCode);
}

function makeValidator(overrides: Partial<ValidationConfig> = {}): ReviewContentValidator {
  return new ReviewContentValidator({
    allowedExtensions: ['.ts', '.js', '.go', '.py'],
    maxFilesChanged:   50,
    maxDiffSizeKB:     300,
    aiRetryAttempts:   3,
    ...overrides,
  });
}

function makeValidDiff(filenames: string[] = ['src/foo.ts']): string {
  return filenames
    .map(f => `--- a/${f}\n+++ b/${f}\n@@ -1,1 +1,2 @@\n context\n+added\n`)
    .join('\n');
}

function makeValidReview(): string {
  return [
    '## Code Review: MR !42',
    '',
    '### Findings',
    '',
    'No critical issues found in the changed code.',
    '',
    '### Verdict',
    '',
    'LGTM',
    '',
    '**Reasoning**: Code is well-structured and follows conventions.',
  ].join('\n');
}

describe('ReviewContentValidator.validateDiff', () => {
  const v = makeValidator();

  test('throws EMPTY_DIFF for empty string', () => {
    assertValidationCode(() => v.validateDiff(''), 'EMPTY_DIFF');
  });

  test('throws DIFF_TOO_LARGE when size exceeds limit', () => {
    const huge = 'x'.repeat(310 * 1024);
    assertValidationCode(() => makeValidator({ maxDiffSizeKB: 1 }).validateDiff(huge), 'DIFF_TOO_LARGE');
  });

  test('throws TOO_MANY_FILES when file count exceeds limit', () => {
    const diff = makeValidDiff(['a.ts', 'b.ts', 'c.ts']);
    assertValidationCode(() => makeValidator({ maxFilesChanged: 2 }).validateDiff(diff), 'TOO_MANY_FILES');
  });

  test('throws NO_SUPPORTED_FILES for unsupported extensions only', () => {
    const diff = '--- a/file.xyz\n+++ b/file.xyz\n@@ -1,1 +1,2 @@\n content\n+added\n';
    assertValidationCode(() => v.validateDiff(diff), 'NO_SUPPORTED_FILES');
  });

  test('returns result with correct file counts for valid diff', () => {
    const diff = makeValidDiff(['a.ts', 'b.go']);
    const result = v.validateDiff(diff);
    expect(result.filesChanged).toBe(2);
    expect(result.supportedFiles).toBe(2);
  });

  test('counts sizeKB correctly', () => {
    const diff = makeValidDiff(['a.ts']);
    const result = v.validateDiff(diff);
    expect(result.sizeKB).toBeGreaterThan(0);
    expect(result.sizeKB).toBeLessThan(1);
  });
});

describe('ReviewContentValidator.validateContent', () => {
  const v = makeValidator();

  test('throws REVIEW_TOO_SHORT for short content', () => {
    assertValidationCode(() => v.validateContent('short'), 'REVIEW_TOO_SHORT');
  });

  test('throws MISSING_SECTION when ## is absent', () => {
    assertValidationCode(() => v.validateContent('a'.repeat(200) + ' Findings Verdict'), 'MISSING_SECTION');
  });

  test('throws MISSING_SECTION when Findings is absent', () => {
    assertValidationCode(() => v.validateContent('## Review\n' + 'x'.repeat(150) + '\nVerdict'), 'MISSING_SECTION');
  });

  test('throws MISSING_SECTION when Verdict is absent', () => {
    assertValidationCode(() => v.validateContent('## Review\n' + 'x'.repeat(150) + '\nFindings'), 'MISSING_SECTION');
  });

  test('passes for valid review', () => {
    expect(() => v.validateContent(makeValidReview())).not.toThrow();
  });
});

describe('ReviewContentValidator.sanitize', () => {
  const v = makeValidator();

  test('escapes < and >', () => {
    const result = v.sanitize('<div>hello</div>');
    expect(result).toContain('&lt;');
    expect(result).toContain('&gt;');
    expect(result).not.toContain('<div>');
  });

  test('removes javascript: patterns', () => {
    const result = v.sanitize('click javascript:alert(1)');
    expect(result).not.toContain('javascript:');
  });

  test('removes <script tags', () => {
    const result = v.sanitize('<script>alert(1)</script>');
    expect(result).not.toContain('<script');
    expect(result).not.toContain('</script');
  });

  test('removes inline event handlers', () => {
    const result = v.sanitize('<img onload="evil()">');
    expect(result).not.toContain('onload=');
  });

  test('leaves clean content unchanged (modulo escaping)', () => {
    const clean = 'This is a safe review with no HTML.';
    expect(v.sanitize(clean)).toBe(clean);
  });
});

describe('ReviewContentValidator env helpers', () => {
  afterEach(() => {
    delete process.env.CI_PIPELINE_SOURCE;
    delete process.env.CI_PROJECT_ID;
    delete process.env.MRI_PROJECT_ID;
    delete process.env.CI_MERGE_REQUEST_IID;
    delete process.env.MRI_MR_IID;
  });

  test('isCrossRepoMode returns true when CI_PIPELINE_SOURCE=trigger', () => {
    process.env.CI_PIPELINE_SOURCE = 'trigger';
    expect(makeValidator().isCrossRepoMode()).toBe(true);
  });

  test('getProjectId uses MRI_PROJECT_ID in cross-repo mode', () => {
    process.env.CI_PIPELINE_SOURCE = 'trigger';
    process.env.MRI_PROJECT_ID = '999';
    expect(makeValidator().getProjectId()).toBe('999');
  });

  test('getProjectId uses CI_PROJECT_ID in local mode', () => {
    process.env.CI_PROJECT_ID = '123';
    expect(makeValidator().getProjectId()).toBe('123');
  });
});
