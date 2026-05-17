import * as fs from 'fs';
import * as path from 'path';
import * as yaml from 'js-yaml';
import type { DocFile, LoadedProject, ServiceRegistry, SystemProject } from '../types';
import type { IProjectLoader } from '../interfaces/IProjectLoader';

export class YamlProjectLoader implements IProjectLoader {
  private readonly dir: string;

  constructor(projectsDir: string = '../projects') {
    this.dir = path.resolve(projectsDir);
  }

  isAvailable(): boolean {
    return (
      fs.existsSync(this.dir) &&
      fs.existsSync(path.join(this.dir, 'registry.yaml'))
    );
  }

  async load(serviceName: string, serviceType: string): Promise<LoadedProject> {
    const registry    = this.loadRegistry();
    const systemName  = registry.services[serviceName] ?? registry.defaultSystem;
    const system      = this.loadSystemProject(systemName);
    const resolved    = system.serviceTypeOverrides?.[serviceName] ?? serviceType ?? system.defaultServiceType;
    const shared      = this.loadMdFiles(path.join(this.dir, '_shared'));
    const sharedTyped = this.loadMdFiles(path.join(this.dir, '_shared', resolved));
    const systemDocs  = this.loadMdFiles(path.join(this.dir, systemName)).filter(d => d.filename !== 'system.yaml');

    return {
      system,
      sharedDocContents: [...shared, ...sharedTyped],
      systemDocContents: systemDocs,
      resolvedServiceType: resolved,
    };
  }

  private loadRegistry(): ServiceRegistry {
    const registryPath = path.join(this.dir, 'registry.yaml');
    const parsed = yaml.load(fs.readFileSync(registryPath, 'utf-8')) as ServiceRegistry;
    if (!parsed?.defaultSystem || !parsed?.services) {
      throw new Error('Invalid registry.yaml: missing defaultSystem or services');
    }
    return parsed;
  }

  private loadSystemProject(systemName: string): SystemProject {
    const systemPath = path.join(this.dir, systemName, 'system.yaml');
    const parsed = yaml.load(fs.readFileSync(systemPath, 'utf-8')) as SystemProject;
    if (!parsed?.name || !parsed?.defaultServiceType) {
      throw new Error(`Invalid system.yaml in ${systemName}: missing name or defaultServiceType`);
    }
    return parsed;
  }

  private loadMdFiles(dirPath: string): DocFile[] {
    if (!fs.existsSync(dirPath)) return [];
    return fs
      .readdirSync(dirPath)
      .filter(f => f.endsWith('.md'))
      .sort()
      .map(filename => ({
        filename,
        content: fs.readFileSync(path.join(dirPath, filename), 'utf-8'),
      }));
  }
}
