export function truncateOutput(output: string): string {
  if (!output) return output;

  const lines = output.split('\n');
  const count = lines.length;

  if (count <= 40) {
    return output;
  }

  const first = lines.slice(0, 20);
  const last = lines.slice(count - 20);
  const omitted = count - 40;
  return [...first, `\n... ${omitted} lines omitted ...\n`, ...last].join('\n');
}
