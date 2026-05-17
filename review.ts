#!/usr/bin/env node
import { loadConfig } from './src/config/ReviewConfig';
import { createReviewer } from './src/factory';

(async () => {
  const config   = loadConfig();
  const reviewer = createReviewer(config);
  await reviewer.run();
  process.exit(0);
})();
