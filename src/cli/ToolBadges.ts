import chalk from 'chalk';

interface BadgeConfig {
  label: string;
  bg: any;
}

const badgeMap: Record<string, BadgeConfig> = {
  execute_bash: { label: 'SHELL', bg: chalk.bgBlue },
  file_read: { label: 'READ', bg: chalk.bgMagenta },
  file_glob: { label: 'LIST', bg: chalk.bgCyan },
  list_dir: { label: 'LIST', bg: chalk.bgCyan },
  file_write: { label: 'EDIT', bg: chalk.bgGreen },
  replace_file_content: { label: 'EDIT', bg: chalk.bgGreen },
  multi_replace_file_content: { label: 'EDIT', bg: chalk.bgGreen },
  manage_task: { label: 'TASK', bg: chalk.bgYellow },
  view_file: { label: 'READ', bg: chalk.bgMagenta },
  run_command: { label: 'SHELL', bg: chalk.bgBlue },
};

const defaultBadge: BadgeConfig = { label: 'INFO', bg: chalk.bgGray };

export function renderBadge(toolName: string, detail: string): string {
  const config = badgeMap[toolName] || defaultBadge;
  const badgeText = ` ${config.label.padEnd(5, ' ')} `;
  return '\n' + config.bg.white.bold(badgeText) + ' ' + chalk.dim(`[${detail}]`);
}
