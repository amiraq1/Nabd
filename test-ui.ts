import { NabdCLI } from './src/cli/NabdCLI.js';
import { globalEventBus } from './src/core/events/EventBus.js';
import { AgentLoop } from './src/core/agent/AgentLoop.js';
import { toolEngine } from './src/core/tool-engine.js';
import { todoStore } from './src/core/state/TodoStore.js';
import chalk from 'chalk';

const fakeLoop = {} as AgentLoop;
const cli = new NabdCLI(fakeLoop);

const callId1 = 'call-1';
globalEventBus.emit({
  type: 'ToolCall',
  toolName: 'file_read',
  arguments: { path: 'package.json' },
  callId: callId1,
  traceId: '1', sessionId: '1', timestamp: Date.now()
} as any);
globalEventBus.emit({
  type: 'ToolResult',
  toolName: 'file_read',
  result: "{\n  \"name\": \"test\"\n}",
  callId: callId1,
  traceId: '1', sessionId: '1', timestamp: Date.now()
} as any);

const callId2 = 'call-2';
globalEventBus.emit({
  type: 'ToolCall',
  toolName: 'list_dir',
  arguments: { path: 'src/core/' },
  callId: callId2,
  traceId: '1', sessionId: '1', timestamp: Date.now()
} as any);
globalEventBus.emit({
  type: 'ToolResult',
  toolName: 'list_dir',
  result: [
    { name: 'tools/', isDir: true },
    { name: 'agent/', isDir: true },
    { name: 'index.ts', isDir: false }
  ],
  callId: callId2,
  traceId: '1', sessionId: '1', timestamp: Date.now()
} as any);

const callId3 = 'call-3';
globalEventBus.emit({
  type: 'ToolCall',
  toolName: 'write_todos',
  arguments: { items: ['Step 1', 'Step 2'] },
  callId: callId3,
  traceId: '1', sessionId: 's1', timestamp: Date.now()
} as any);
todoStore.setAll('s1', ['Step 1', 'Step 2']);
globalEventBus.emit({
  type: 'ToolResult',
  toolName: 'write_todos',
  result: "TODOS updated",
  callId: callId3,
  traceId: '1', sessionId: 's1', timestamp: Date.now()
} as any);

process.exit(0);
