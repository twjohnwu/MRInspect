import { MRReviewer } from '../src/review/MRReviewer';
import type { IAIProvider } from '../src/interfaces/IAIProvider';
import type { IProjectLoader } from '../src/interfaces/IProjectLoader';
import type { IPromptComposer } from '../src/interfaces/IPromptComposer';
import type { IReviewValidator } from '../src/interfaces/IReviewValidator';
import type { Config, LoadedProject } from '../src/types';

const profile: LoadedProject = {
  system: {
    name: 'test',
    description: 'test project',
    defaultServiceType: 'backend',
    frameworks: [],
  },
  sharedDocContents: [],
  systemDocContents: [],
  resolvedServiceType: 'backend',
};

function makeReviewer(result: string, validateContent: (content: string) => void): MRReviewer {
  const reviewer = Object.create(MRReviewer.prototype) as MRReviewer;
  const profileLoader: IProjectLoader = {
    isAvailable: () => true,
    load: async () => profile,
  };
  const promptComposer = {
    composeSelfReflection: (_profile: LoadedProject, review: string) => `reflect:${review}`,
  } as IPromptComposer;
  const aiProvider: IAIProvider = {
    name: 'fake',
    generate: async () => result,
  };
  const validator = { validateContent } as IReviewValidator;
  const config = {
    aiProvider: 'openai',
    service: { name: 'test', type: 'backend' },
    providers: {
      openai: { model: 'test-model', maxTokens: 100, temperature: 0 },
    },
  } as Config;

  (reviewer as any).profileLoader = profileLoader;
  (reviewer as any).promptComposer = promptComposer;
  (reviewer as any).aiProvider = aiProvider;
  (reviewer as any).validator = validator;
  (reviewer as any).config = config;
  return reviewer;
}

async function selfReflect(reviewer: MRReviewer, review: string): Promise<string> {
  return (reviewer as any).selfReflect(review);
}

describe('MRReviewer.selfReflect validation guard', () => {
  const original = '## Code Review\n## Findings\noriginal\n## Verdict\nApproved';

  test('garbage reflection keeps the original review when validation fails', async () => {
    const reviewer = makeReviewer('garbage, not a review', () => {
      throw new Error('missing required review sections');
    });

    await expect(selfReflect(reviewer, original)).resolves.toBe(original);
  });

  test('valid preambled reflection returns cleaned validated content', async () => {
    const cleaned = '## Code Review\n## Findings\nnew finding\n## Verdict\nChanges requested';
    const preambled = `Sure — here's the improved review:\n\n${cleaned}`;
    const validated: string[] = [];
    const reviewer = makeReviewer(preambled, (content) => validated.push(content));

    await expect(selfReflect(reviewer, original)).resolves.toBe(cleaned);
    expect(validated).toEqual([cleaned]);
  });
});
