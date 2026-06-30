export const DESTRUCTIVE_PATTERNS: RegExp[] = [
  /rm\s+-r/i,
  /rm\s+-f/i,
  /mkfs/i,
  /dd\s+of=/i,
  />\s*\/dev\/sd/i,
  /git\s+push\s+--force/i,
  /chmod\s+777/i,
  /sudo/i,
  />\s*\/etc\/passwd/i
];

export function isDestructive(command: string): boolean {
  if (!command) return false;
  return DESTRUCTIVE_PATTERNS.some(pattern => pattern.test(command));
}
