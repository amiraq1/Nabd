import fs from 'node:fs';
import path from 'node:path';
import type { ToolContext, ToolDefinition, ExecutionPolicy } from '../types.js';

export const listDirSchema = {
  name: 'list_dir',
  description: 'Lists files and directories in a given path.',
  parameters: {
    type: 'object',
    properties: {
      path: {
        type: 'string',
        description: 'The directory path to list.',
      },
    },
    required: ['path'],
  },
};

export const listDirTool: ToolDefinition = {
  ...listDirSchema,
  id: 'tool-list_dir-v1',
  version: '1.0.0',
  category: 'filesystem',
  visibility: 'stable',
  permissions: ['filesystem'],
  aliases: ['ls', 'dir'],
  getPolicy: () => ({
    allowFilesystemWrite: false,
    allowDelete: false,
    allowBackgroundProcess: false,
    workingDirectory: process.cwd(),
    environment: {},
    allowedCommands: [],
  }),
  execute: async (
    args: unknown,
    context: ToolContext,
  ): Promise<any> => {
    const { path: dirPath } = args as { path: string };
    
    if (typeof dirPath !== 'string' || dirPath.trim().length === 0) {
      return { error: 'list_dir: path must be a non-empty string' };
    }

    const resolvedPath = path.resolve(process.cwd(), dirPath);
    if (!resolvedPath.startsWith(process.cwd())) {
      return { error: 'list_dir: path traversal outside cwd is not allowed' };
    }

    try {
      const dirents = fs.readdirSync(resolvedPath, { withFileTypes: true });
      const entries = dirents.map(d => ({
        name: d.name,
        isDir: d.isDirectory()
      }));
      return { path: dirPath, entries, children: entries };
    } catch (err: any) {
      return { error: `list_dir: failed to read directory - ${err.message}` };
    }
  },
};
