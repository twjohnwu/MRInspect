import * as path from 'path';
import { YamlProjectLoader } from '../src/project/YamlProjectLoader';

// Uses the real projects directory from mrinspect/projects/
const PROJECTS_DIR = path.resolve(__dirname, '../projects');

function makeLoader(): YamlProjectLoader {
  return new YamlProjectLoader(PROJECTS_DIR);
}

describe('YamlProjectLoader', () => {
  test('isAvailable() returns true for real projects dir', () => {
    expect(makeLoader().isAvailable()).toBe(true);
  });

  test('isAvailable() returns false for non-existent dir', () => {
    expect(new YamlProjectLoader('/does/not/exist').isAvailable()).toBe(false);
  });

  test('loads margherita-pizza profile by name', async () => {
    const loader = makeLoader();
    const p = await loader.load('dough-service', 'backend');
    expect(p.system.name).toBeTruthy();
    expect(p.resolvedServiceType).toBeTruthy();
  });

  test('falls back to defaultSystem for unknown service', async () => {
    const loader = makeLoader();
    const p = await loader.load('nonexistent-service-xyz', 'backend');
    expect(p.system.name).toBeTruthy();
  });

  test('shared docs are loaded', async () => {
    const loader = makeLoader();
    const p = await loader.load('dough-service', 'backend');
    expect(p.sharedDocContents.length).toBeGreaterThan(0);
  });

  test('system docs are loaded', async () => {
    const loader = makeLoader();
    const p = await loader.load('dough-service', 'backend');
    expect(p.systemDocContents.length).toBeGreaterThan(0);
  });

  test('system docs do not include system.yaml', async () => {
    const loader = makeLoader();
    const p = await loader.load('dough-service', 'backend');
    const hasYaml = p.systemDocContents.some(d => d.filename === 'system.yaml');
    expect(hasYaml).toBe(false);
  });

  test('all doc files have non-empty content', async () => {
    const loader = makeLoader();
    const p = await loader.load('dough-service', 'backend');
    const allDocs = [...p.sharedDocContents, ...p.systemDocContents];
    for (const doc of allDocs) {
      expect(doc.content.trim().length).toBeGreaterThan(0);
    }
  });
});
