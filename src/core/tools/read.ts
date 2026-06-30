import fs from 'node:fs';
import path from 'node:path';
import type { ToolContext, ToolDefinition, ExecutionPolicy } from '../types.js';
import { ProcessSession } from '../ProcessSession.js';

export const fileReadSchema = {
  name: 'file_read',
  description: 'Reads the contents of a file as UTF-8 text.',
  parameters: {
    type: 'object',
    properties: {
      path: {
        type: 'string',
        description: 'The path to the file to read.',
      },
    },
    required: ['path'],
  },
};

export const fileReadTool: ToolDefinition = {
  ...fileReadSchema,
  id: 'tool-file_read-v1',
  version: '1.0.0',
  category: 'filesystem',
  visibility: 'stable',
  permissions: ['filesystem'],
  aliases: ['read_file', 'cat'],
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
    const { path: filePath } = args as { path: string };
    
    if (typeof filePath !== 'string' || filePath.trim().length === 0) {
      return { error: 'file_read: path must be a non-empty string' };
    }

    const resolvedPath = path.resolve(process.cwd(), filePath);
    if (!resolvedPath.startsWith(process.cwd())) {
      return { error: 'file_read: path traversal outside cwd is not allowed' };
    }

    try {
      const content = fs.readFileSync(resolvedPath, 'utf-8');
      const lineCount = content.split('\n').length;
      return { content, lineCount, path: filePath, text: content };
    } catch (err: any) {
      return { error: `file_read: failed to read file - ${err.message}` };
    }
  },
};
