import { MRReviewer } from '../src/review/MRReviewer';

// cleanResponse takes no constructor dependencies, so exercise it through
// the smallest seam: an uninitialized instance built off the class
// prototype, calling the private method via bracket access.
function makeReviewer(): MRReviewer {
  return Object.create(MRReviewer.prototype) as MRReviewer;
}

function cleanResponse(reviewer: MRReviewer, response: string): string {
  return (reviewer as any).cleanResponse(response);
}

describe('MRReviewer.cleanResponse — earliest marker wins', () => {
  test('tail-quoted high-priority marker hijack does not discard the real review', () => {
    const response =
      'Some preamble text before the real heading.\n\n' +
      '## Review\nThis is the real review body with actual findings.\n' +
      'More real review content here.\n\n' +
      '```diff\n+ ## Code Review\n+ some quoted diff line from the MR\n```\n';
    const got = cleanResponse(makeReviewer(), response);
    expect(got).toContain('This is the real review body with actual findings.');
    expect(got.startsWith('```diff')).toBe(false);
  });

  test('earliest position beats list priority', () => {
    const response = 'noise\n### MR Info\nreal content\nmore noise\n## Code Review\nlater section';
    const got = cleanResponse(makeReviewer(), response);
    expect(got.startsWith('### MR Info')).toBe(true);
  });
});
