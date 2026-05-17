import { convertChangesToDiff } from '../src/diff/ApiDiffFetcher';
import type { Change } from '../src/types';

function makeChange(overrides: Partial<Change>): Change {
  return {
    oldPath:     overrides.oldPath     ?? 'file.ts',
    newPath:     overrides.newPath     ?? 'file.ts',
    diff:        overrides.diff        ?? '@@ -1,3 +1,4 @@\n context\n+added\n',
    newFile:     overrides.newFile     ?? false,
    renamedFile: overrides.renamedFile ?? false,
    deletedFile: overrides.deletedFile ?? false,
  };
}

describe('convertChangesToDiff', () => {
  test('returns empty string for empty input', () => {
    expect(convertChangesToDiff([])).toBe('');
  });

  test('modified file uses a/b paths', () => {
    const result = convertChangesToDiff([makeChange({ oldPath: 'src/foo.ts', newPath: 'src/foo.ts' })]);
    expect(result).toContain('--- a/src/foo.ts');
    expect(result).toContain('+++ b/src/foo.ts');
  });

  test('new file uses /dev/null as source', () => {
    const result = convertChangesToDiff([makeChange({ newFile: true, newPath: 'src/new.ts' })]);
    expect(result).toContain('--- /dev/null');
    expect(result).toContain('+++ b/src/new.ts');
  });

  test('deleted file uses /dev/null as destination', () => {
    const result = convertChangesToDiff([makeChange({ deletedFile: true, oldPath: 'src/old.ts' })]);
    expect(result).toContain('--- a/src/old.ts');
    expect(result).toContain('+++ /dev/null');
  });

  test('renamed file shows old and new paths', () => {
    const result = convertChangesToDiff([
      makeChange({ renamedFile: true, oldPath: 'src/old.ts', newPath: 'src/renamed.ts' }),
    ]);
    expect(result).toContain('--- a/src/old.ts');
    expect(result).toContain('+++ b/src/renamed.ts');
  });

  test('appends newline to diff body if missing', () => {
    const c = makeChange({ diff: '@@ -1,1 +1,1 @@\n-old\n+new' });
    const result = convertChangesToDiff([c]);
    expect(result.endsWith('\n')).toBe(true);
  });

  test('handles multiple changes', () => {
    const changes: Change[] = [
      makeChange({ newFile: true, newPath: 'a.ts' }),
      makeChange({ deletedFile: true, oldPath: 'b.ts' }),
    ];
    const result = convertChangesToDiff(changes);
    expect(result).toContain('/dev/null');
    expect(result).toContain('a.ts');
    expect(result).toContain('b.ts');
  });
});
