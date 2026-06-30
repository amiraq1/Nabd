import fs from 'node:fs';
import path from 'node:path';
import type { ToolContext, ToolDefinition } from '../types.js';

// الحد الأقصى الآمن لقراءة الملفات في الذاكرة لبيئة Termux (1 ميجابايت)
const MAX_READ_BYTES = 1024 * 1024; 

export const fileReadSchema = {
  name: 'file_read',
  description: `Reads the contents of a file. Fails if file > ${MAX_READ_BYTES / 1024}KB to prevent memory exhaustion.`,
  parameters: {
    type: 'object',
    properties: { path: { type: 'string' } },
    required: ['path'],
  },
};

export const fileReadTool: ToolDefinition = {
  ...fileReadSchema,
  id: 'tool-file_read-v2',
  version: '2.0.0',
  category: 'filesystem',
  visibility: 'stable',
  permissions: ['filesystem'],
  aliases: ['read_file', 'cat'],
  getPolicy: () => ({ workingDirectory: process.cwd() }),
  
  execute: async (args: unknown, context: ToolContext): Promise<any> => {
    const { path: filePath } = args as { path: string };

    if (typeof filePath !== 'string' || filePath.trim() === '') {
      return { error: 'يجب توفير مسار ملف صالح.' };
    }

    const cwd = process.cwd();
    const resolvedPath = path.resolve(cwd, filePath);
    
    // سد ثغرة العبور الكاذب (False Traversal Bypass) عبر فحص فاصل المجلدات
    if (!resolvedPath.startsWith(cwd + path.sep) && resolvedPath !== cwd) {
      return { error: 'محاولة وصول غير مصرح بها: لا يمكن قراءة ملفات خارج مساحة العمل.' };
    }

    try {
      const stats = fs.statSync(resolvedPath);
      if (!stats.isFile()) {
        return { error: 'المسار المطلوب ليس ملفاً (ربما هو مجلد).' };
      }

      if (stats.size > MAX_READ_BYTES) {
        return { error: `الملف ضخم جداً (${(stats.size / 1024 / 1024).toFixed(2)} MB). أقصى حد مسموح هو 1 MB. استخدم أدوات bash مثل head, tail, أو grep بدلاً من ذلك.` };
      }

      const content = fs.readFileSync(resolvedPath, 'utf-8');
      return { content, lineCount: content.split('\n').length, path: filePath };
    } catch (err: any) {
      return { error: `فشل قراءة الملف: ${err.message}` };
    }
  },
};
