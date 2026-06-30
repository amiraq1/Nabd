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
  id: 'tool-list_dir-v2',
  version: '2.0.0',
  category: 'filesystem',
  visibility: 'stable',
  permissions: ['filesystem'],
  aliases: ['ls', 'dir'],
  getPolicy: () => ({ workingDirectory: process.cwd() }),
  
  execute: async (args: unknown, context: ToolContext): Promise<any> => {
    const { path: dirPath } = args as { path: string };

    if (typeof dirPath !== 'string' || dirPath.trim() === '') {
      return { error: 'يجب توفير مسار مجلد صالح.' };
    }

    const cwd = process.cwd();
    const resolvedPath = path.resolve(cwd, dirPath);
    
    // سد ثغرة العبور الكاذب
    if (!resolvedPath.startsWith(cwd + path.sep) && resolvedPath !== cwd) {
      return { error: 'محاولة وصول غير مصرح بها: لا يمكن استعراض مجلدات خارج مساحة العمل.' };
    }

    try {
      const dirents = fs.readdirSync(resolvedPath, { withFileTypes: true });
      const entries = dirents.map(d => ({
        name: d.name,
        isDir: d.isDirectory()
      }));
      return { path: dirPath, entries };
    } catch (err: any) {
      return { error: `فشل استعراض المجلد: ${err.message}` };
    }
  },
};
