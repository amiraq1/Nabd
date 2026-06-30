import type { ToolContext, ToolDefinition } from '../types.js';
import { ProcessSession } from '../ProcessSession.js';
import { todoStore } from '../state/TodoStore.js';

function createInstantSession(toolName: string, output: string): ProcessSession {
  const session = new ProcessSession({
    executionId: `exec-${toolName}-${Date.now()}`,
    command: toolName,
    args: [],
    cwd: process.cwd(),
  });
  session.start(Math.floor(Math.random() * 10000) + 1);
  session.appendStdout(output + '\n');
  session.finish(0, null);
  return session;
}

function createErrorSession(toolName: string, error: string): ProcessSession {
  const session = new ProcessSession({
    executionId: `exec-${toolName}-err-${Date.now()}`,
    command: toolName,
    args: [],
    cwd: process.cwd(),
  });
  session.error(new Error(error));
  return session;
}

export const writeTodosTool: ToolDefinition = {
  name: 'write_todos',
  id: 'tool-write_todos-v1',
  version: '1.0.0',
  category: 'system',
  visibility: 'stable',
  permissions: [],
  aliases: ['todos_create'],
  description: 'Creates a new TODOS checklist, replacing any existing one.',
  parameters: {
    type: 'object',
    properties: {
      items: {
        type: 'array',
        items: { type: 'string' },
        description: 'Array of task descriptions.',
      },
    },
    required: ['items'],
  },
  getPolicy: () => ({}),
  execute: async (args: unknown, context: ToolContext): Promise<ProcessSession> => {
    const { items } = args as { items: string[] };
    if (!Array.isArray(items) || items.length === 0) {
      return createErrorSession('write_todos', 'items must be a non-empty array');
    }
    const sessionId = context.security?.sessionId || 'default';
    todoStore.setAll(sessionId, items);
    return createInstantSession('write_todos', `TODOS updated: ${items.length} items`);
  },
};

export const updateTodoTool: ToolDefinition = {
  name: 'update_todo',
  id: 'tool-update_todo-v1',
  version: '1.0.0',
  category: 'system',
  visibility: 'stable',
  permissions: [],
  aliases: ['todo_check'],
  description: 'Marks a TODO item as done or pending.',
  parameters: {
    type: 'object',
    properties: {
      index: {
        type: 'number',
        description: '0-based index of the TODO item.',
      },
      done: {
        type: 'boolean',
        description: 'true to mark done, false to mark pending.',
      },
    },
    required: ['index', 'done'],
  },
  getPolicy: () => ({}),
  execute: async (args: unknown, context: ToolContext): Promise<ProcessSession> => {
    const { index, done } = args as { index: number; done: boolean };
    if (typeof index !== 'number' || typeof done !== 'boolean') {
      return createErrorSession('update_todo', 'Invalid arguments');
    }
    const sessionId = context.security?.sessionId || 'default';
    let success = false;
    if (done) {
      success = todoStore.markDone(sessionId, index);
    } else {
      success = todoStore.markPending(sessionId, index);
    }
    
    if (!success) {
      return createErrorSession('update_todo', `Invalid TODO index: ${index}`);
    }
    return createInstantSession('update_todo', `TODO #${index} marked ${done ? 'done' : 'pending'}`);
  },
};
