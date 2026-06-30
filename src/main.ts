#!/usr/bin/env node
import { globalEventBus } from './core/events/EventBus.js';
import { semanticMemory } from './core/memory/SemanticMemory.js';
import { toolEngine } from './core/tool-engine.js';
import { executeBashTool } from './core/tools/bash.js';
import { writeTodosTool, updateTodoTool } from './core/tools/todos.js';
import { fileReadTool } from './core/tools/read.js';
import { listDirTool } from './core/tools/list.js';
import { inferenceManager } from './core/inference/InferenceManager.js';
import { OllamaProvider } from './core/inference/providers/OllamaProvider.js';
import { OpenAICompatibleProvider } from './core/inference/providers/OpenAICompatibleProvider.js';
import { globalConfig } from './GlobalConfig.js';
import { AgentLoop } from './core/agent/AgentLoop.js';
import { NabdCLI } from './cli/NabdCLI.js';
import type { SecurityContext } from './core/security/SecurityContext.js';
import { randomUUID } from 'node:crypto';

async function bootstrap() {
  // 1. globalEventBus is a singleton and already initialized.

  // 2. SemanticMemory automatically loaded in constructor.
  console.log('SemanticMemory loaded.');

  // 3. Register tools
  toolEngine.register(executeBashTool);
  toolEngine.register(writeTodosTool);
  toolEngine.register(updateTodoTool);
  toolEngine.register(fileReadTool);
  toolEngine.register(listDirTool);
  console.log('Tools registered.');

  // 4. Initialize Inference Provider based on Config
  if (globalConfig.provider === 'nvidia' || globalConfig.provider === 'openai') {
    const cloudProvider = new OpenAICompatibleProvider();
    await cloudProvider.initialize();
    inferenceManager.registerProvider(cloudProvider);
    console.log(`Inference Provider (${globalConfig.provider}) registered.`);
  } else {
    const ollama = new OllamaProvider();
    await ollama.initialize();
    inferenceManager.registerProvider(ollama);
    console.log('Inference Provider (Ollama) registered.');
  }

  // 5. Instantiate AgentLoop
  const security: SecurityContext = {
    role: 'ROOT_AGENT',
    permissions: ['system', 'filesystem', 'dangerous', 'network'],
    sessionId: randomUUID(),
    workspaceRoot: process.cwd(),
    networkPolicy: 'allow',
    filesystemPolicy: 'read_write',
    createdAt: Date.now()
  };

  const agentLoop = new AgentLoop(toolEngine, security, globalEventBus);

  // 6. Instantiate and start CLI
  const cli = new NabdCLI(agentLoop);
  
  // Setup Graceful Shutdown
  const shutdown = (signal: string) => {
    // Clear ANSI formatting and restore cursor
    process.stdout.write('\x1b[0m\x1b[?25h\n');
    console.log(`\nReceived ${signal}. Gracefully shutting down NABD_OS...`);
    
    // Save memory to disk safely
    semanticMemory.forceSave();
    
    // In a real scenario, we'd also cancel any pending AgentLoop iterations
    // and shutdown the connection pool:
    // (ollama as any).pool.destroy();
    
    process.exit(0);
  };

  process.on('SIGINT', () => shutdown('SIGINT'));
  process.on('SIGTERM', () => shutdown('SIGTERM'));
  
  process.on('uncaughtException', (err) => {
    process.stdout.write('\x1b[0m\x1b[?25h\n');
    console.error('\nUNCAUGHT EXCEPTION:', err);
    semanticMemory.forceSave();
    process.exit(1);
  });

  // Start the UI
  cli.start();
}

bootstrap().catch(err => {
  console.error('Fatal Boot Error:', err);
  process.exit(1);
});
