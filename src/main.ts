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
  console.log('SemanticMemory loaded.');

  toolEngine.register(executeBashTool);
  toolEngine.register(writeTodosTool);
  toolEngine.register(updateTodoTool);
  toolEngine.register(fileReadTool);
  toolEngine.register(listDirTool);
  console.log('Tools registered.');

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

  const security: SecurityContext = {
    role: 'ROOT_AGENT',
    permissions: ['system', 'filesystem', 'dangerous', 'network'],
    sessionId: randomUUID(), // توليد معرف جلسة موحد
    workspaceRoot: process.cwd(),
    networkPolicy: 'allow',
    filesystemPolicy: 'read_write',
    createdAt: Date.now()
  };

  const agentLoop = new AgentLoop(toolEngine, security, globalEventBus);
  const cli = new NabdCLI(agentLoop);

  // علم (Flag) لمنع استدعاء الإغلاق عدة مرات متزامنة
  let isShuttingDown = false;

  /**
   * الإغلاق الآمن (Graceful Shutdown)
   * يضمن عدم قطع العمليات الحيوية للكتابة على القرص قبل الخروج
   */
  const shutdown = async (signal: string) => {
    if (isShuttingDown) return;
    isShuttingDown = true;

    process.stdout.write('\x1b[0m\x1b[?25h\n');
    console.log(`\n[${signal}] جارٍ إغلاق NABD_OS بأمان، يرجى الانتظار...`);

    try {
      // انتظار الحفظ غير المتزامن (Async) لضمان كتابة الذاكرة دون تشويه الـ JSON
      await Promise.resolve(semanticMemory.forceSave());
      
      // هنا يمكن مستقبلاً إضافة: await agentLoop.shutdown() لتفريغ الـ Queue
    } catch (err) {
      console.error('حدث خطأ أثناء حفظ الذاكرة خلال الإغلاق:', err);
    } finally {
      console.log('تم الإغلاق بنجاح.');
      process.exit(0);
    }
  };

  process.on('SIGINT', () => void shutdown('SIGINT'));
  process.on('SIGTERM', () => void shutdown('SIGTERM'));

  process.on('uncaughtException', async (err) => {
    process.stdout.write('\x1b[0m\x1b[?25h\n');
    console.error('\nخطأ غير ملتقط (UNCAUGHT EXCEPTION):', err);
    await shutdown('FATAL_ERROR');
  });

  cli.start();
}

bootstrap().catch(err => {
  console.error('خطأ جسيم أثناء الإقلاع:', err);
  process.exit(1);
});
