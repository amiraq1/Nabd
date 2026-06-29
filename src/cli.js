#!/usr/bin/env node
import { createAgent } from './agent.js';
import { loadConfig } from './config.js';

async function main() {
  const args = process.argv.slice(2);
  const config = await loadConfig();
  const agent = createAgent(config);

  if (args.length === 0) {
    console.log(`Termux AI Agent v${config.version}`);
    console.log('Usage: termux-ai-agent <command> [options]');
    console.log('\nCommands:');
    console.log('  run <instruction>    Execute a natural-language task');
    console.log('  shell <command>      Run a shell command safely');
    console.log('  list                 List available skills');
    console.log('  config               Show current configuration');
    process.exit(0);
  }

  const [command, ...rest] = args;
  const instruction = rest.join(' ');

  try {
    switch (command) {
      case 'run':
        if (!instruction) throw new Error('No instruction provided');
        await agent.run(instruction);
        break;
      case 'shell':
        if (!instruction) throw new Error('No shell command provided');
        const shellResult = await agent.shell(instruction);
        console.log(JSON.stringify(shellResult, null, 2));
        break;
      case 'list':
        agent.listSkills();
        break;
      case 'config':
        console.log(JSON.stringify(config, null, 2));
        break;
      default:
        throw new Error(`Unknown command: ${command}`);
    }
  } catch (err) {
    console.error('Error:', err.message);
    process.exit(1);
  }
}

main();
