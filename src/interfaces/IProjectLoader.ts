import type { LoadedProject } from '../types';

export interface IProjectLoader {
  isAvailable(): boolean;
  load(serviceName: string, serviceType: string): Promise<LoadedProject>;
}
