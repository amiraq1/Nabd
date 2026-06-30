export function formatReadResult(content: string | any): { lineCount: number; summary: string } {
  let text = '';
  if (typeof content === 'string') {
    text = content;
  } else if (content && typeof content.text === 'string') {
    text = content.text;
  } else if (content && Array.isArray(content.content)) {
    // MCP style
    const t = content.content.find((c: any) => c.type === 'text');
    text = t ? t.text : JSON.stringify(content);
  } else {
    text = JSON.stringify(content || '');
  }

  const lineCount = text ? text.split('\n').length : 0;
  return {
    lineCount,
    summary: `${lineCount} lines`
  };
}

export function formatListResult(result: any): { summary: string; directories: string[]; files: string[] } {
  let entries: any[] = [];
  if (Array.isArray(result)) {
    entries = result;
  } else if (result && Array.isArray(result.children)) {
    entries = result.children;
  } else if (result && Array.isArray(result.content)) {
    // MCP style where it returns text containing the list, but maybe we can parse it
    try {
      const t = result.content.find((c: any) => c.type === 'text');
      if (t) entries = JSON.parse(t.text);
    } catch {
      // ignore
    }
  }

  const directories: string[] = [];
  const files: string[] = [];

  for (const entry of entries) {
    if (typeof entry === 'string') {
      if (entry.endsWith('/')) directories.push(entry);
      else files.push(entry);
    } else if (entry && typeof entry === 'object') {
      const name = entry.name || entry.path || 'unknown';
      const isDir = entry.isDir === true || entry.type === 'directory' || name.endsWith('/');
      if (isDir) directories.push(name);
      else files.push(name);
    }
  }

  const dirCount = directories.length;
  const fileCount = files.length;
  const total = dirCount + fileCount;

  return {
    summary: `Found ${total} items (${dirCount} dirs, ${fileCount} files)`,
    directories,
    files
  };
}
