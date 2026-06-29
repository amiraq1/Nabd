import { createAgent } from './agent.js';
import { loadConfig } from './config.js';

export { createAgent, loadConfig };

export async function runTask(instruction, options = {}) {
  const config = await loadConfig(options.configPath);
  const agent = createAgent(config);
  return agent.run(instruction);
}
