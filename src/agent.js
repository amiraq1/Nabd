import { parseInstruction } from './parser.js';
import { executeShell } from './executors/shell.js';
import { executeFile } from './executors/file.js';

const skills = {
  shell: { name: 'shell', description: 'Run safe shell commands', executor: executeShell },
  file: { name: 'file', description: 'Read and manage files', executor: executeFile }
};

export function createAgent(config) {
  return {
    async run(instruction) {
      const plan = parseInstruction(instruction);
      console.log(`Plan: ${plan.description}`);

      const results = [];
      for (const step of plan.steps) {
        const skill = skills[step.skill];
        if (!skill) throw new Error(`Unsupported skill: ${step.skill}`);
        const result = await skill.executor(step, config);
        results.push(result);
        console.log(result);
      }
      return results;
    },

    async shell(command) {
      return executeShell({ action: 'exec', command }, config);
    },

    listSkills() {
      console.log('Available skills:');
      for (const skill of Object.values(skills)) {
        console.log(`  ${skill.name} - ${skill.description}`);
      }
    }
  };
}
