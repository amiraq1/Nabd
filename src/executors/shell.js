import { spawn } from 'node:child_process';

export async function executeShell(step, config) {
  const command = step.command;
  if (!command || typeof command !== 'string') {
    throw new Error('Shell executor requires a command string');
  }

  if (config.safeMode) {
    validateCommand(command, config.shell);
  }

  if (config.dryRun) {
    return `[dry-run] would execute: ${command}`;
  }

  const { stdout, stderr, exitCode } = await runCommand(command, config.shell.maxTimeoutMs);
  return {
    command,
    exitCode,
    stdout: stdout.trim(),
    stderr: stderr.trim()
  };
}

function validateCommand(command, shellConfig) {
  for (const pattern of shellConfig.blockedPatterns) {
    const regex = new RegExp(pattern, 'i');
    if (regex.test(command)) {
      throw new Error(`Blocked command pattern: ${pattern}`);
    }
  }

  const baseCommand = command.trim().split(/\s+/)[0];
  const allowed = shellConfig.allowedCommands.some(cmd => {
    if (cmd.endsWith('*')) {
      return baseCommand.startsWith(cmd.slice(0, -1));
    }
    return cmd === baseCommand;
  });

  if (!allowed) {
    throw new Error(`Command not allowed: ${baseCommand}`);
  }
}

function runCommand(command, timeoutMs) {
  return new Promise((resolve, reject) => {
    const child = spawn(command, { shell: true });
    let stdout = '';
    let stderr = '';
    let killed = false;

    const timer = setTimeout(() => {
      killed = true;
      child.kill('SIGTERM');
    }, timeoutMs);

    child.stdout.on('data', data => { stdout += data; });
    child.stderr.on('data', data => { stderr += data; });

    child.on('error', err => {
      clearTimeout(timer);
      reject(err);
    });

    child.on('close', code => {
      clearTimeout(timer);
      if (killed) {
        resolve({ stdout, stderr, exitCode: 'timeout' });
      } else {
        resolve({ stdout, stderr, exitCode: code });
      }
    });
  });
}
