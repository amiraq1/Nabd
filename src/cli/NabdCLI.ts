import readline from 'node:readline';
import chalk from 'chalk';
import type { AgentLoop } from '../core/agent/AgentLoop.js';
import { globalEventBus } from '../core/events/EventBus.js';
import type { SystemEvent } from '../core/events/ExecutionEvent.js';
import { renderBadge } from './ToolBadges.js';
import { ThoughtTracker } from './CollapsibleThought.js';
import { todoStore } from '../core/state/TodoStore.js';
import { formatReadResult, formatListResult } from './ToolResultFormatter.js';
import { globalExpandBuffer } from './ExpandableBuffer.js';

export class NabdCLI {
  private readonly rl: readline.Interface;
  private readonly thoughtTracker = new ThoughtTracker();
  private readonly callDetails = new Map<string, string>();
  
  private currentThoughtId: string | null = null;
  private pendingThoughtText: string = '';
  private awaitingConfirmation: boolean = false;
  // منع الردود المتعددة والمكررة لتأكيدات الأوامر
  private isProcessingInput: boolean = false;

  constructor(private readonly agentLoop: AgentLoop) {
    this.rl = readline.createInterface({
      input: process.stdin,
      output: process.stdout,
      terminal: true
    });
    
    this.rl.setPrompt(chalk.blue('❯ '));

    this.registerKeyBindings();
    this.registerEventHandlers();
    this.registerInputHandlers();
  }

  public start(): void {
    this.rl.prompt();
  }

  // ==========================================
  // Keyboard Binding & Input Handlers
  // ==========================================

  private registerKeyBindings(): void {
    process.stdin.on('keypress', (str, key) => {
      // تفريغ وتوسيع المخزن الأخير للمخرجات
      if (key && key.ctrl && key.name === 'o') {
        this.clearLineAndWrite(() => {
          const lastText = globalExpandBuffer.expandLast() || this.thoughtTracker.getLastThought();
          if (lastText) {
            console.log(chalk.dim('\n* Expanded Content:'));
            console.log(chalk.gray(lastText));
          }
        });
        this.rl.prompt();
      } 
      // معالجة الإنهاء الآمن
      else if (key && key.ctrl && key.name === 'c') {
        if (this.awaitingConfirmation) {
          this.awaitingConfirmation = false;
          this.agentLoop.confirmCommand(false);
          this.rl.setPrompt(chalk.blue('❯ '));
          this.rl.prompt();
        } else {
          process.exit(0);
        }
      }
    });
  }

  private registerInputHandlers(): void {
    this.rl.on('line', async (line) => {
      if (this.isProcessingInput) return;
      
      const input = line.trim();

      if (this.awaitingConfirmation) {
        this.awaitingConfirmation = false;
        this.rl.setPrompt(chalk.blue('❯ ')); 
        
        const answer = input.toLowerCase();
        // دعم لغوي أوسع للإجابة بنعم
        if (['yes', 'y', 'نعم', 'ن'].includes(answer)) {
          this.agentLoop.confirmCommand(true);
        } else {
          this.agentLoop.confirmCommand(false);
        }
        return;
      }

      if (!input) {
        this.rl.prompt();
        return;
      }

      if (input === '/exit' || input === 'خروج') {
        process.exit(0);
      }

      this.isProcessingInput = true;
      try {
        await this.agentLoop.run(input);
      } catch (err) {
        this.clearLineAndWrite(() => {
          console.log(chalk.red('خطأ في النظام: ') + (err as Error).message);
        });
        this.rl.prompt();
      } finally {
        this.isProcessingInput = false;
      }
    });
  }

  // ==========================================
  // Centralized Event Dispatcher
  // ==========================================

  private registerEventHandlers(): void {
    globalEventBus.subscribe((event: SystemEvent) => {
      this.clearLineAndWrite(() => this.handleEvent(event));
    });
  }

  private handleEvent(event: SystemEvent): void {
    // توجيه الحدث إلى الدالة المختصة (Router Pattern)
    switch (event.type) {
      case 'InferenceStarted':
      case 'Token':
      case 'Completed':
        this.handleThoughtStream(event as any);
        break;
      case 'PlannerDecision':
        this.handlePlannerDecision(event as any);
        break;
      case 'ConfirmationRequired':
        this.handleConfirmationRequest();
        break;
      case 'ToolCall':
        this.handleToolCall(event as any);
        break;
      case 'ToolResult':
        this.handleToolResult(event as any);
        break;
      case 'StdoutChunk':
      case 'StderrChunk':
      case 'StdoutBatch':
      case 'StderrBatch':
        this.handleProcessOutput(event as any);
        break;
      case 'Failed':
        console.log(chalk.red(`\n[خطأ جسيم: ${(event as any).error}]`));
        this.rl.prompt();
        break;
    }
  }

  // ==========================================
  // Specific Event Handlers
  // ==========================================

  private handleThoughtStream(event: any): void {
    if (event.type === 'InferenceStarted') {
      const ts = Date.now();
      if (!this.currentThoughtId) {
        this.currentThoughtId = `thought-${ts}`;
        this.thoughtTracker.start();
        this.pendingThoughtText = '';
      }
    } else if (event.type === 'Token') {
      this.pendingThoughtText += event.text || '';
    } else if (event.type === 'Completed' && !('executionId' in event)) {
      this.pendingThoughtText = event.fullText || this.pendingThoughtText;
    }
  }

  private handlePlannerDecision(event: any): void {
    if (this.currentThoughtId) {
      const result = this.thoughtTracker.finish(this.pendingThoughtText);
      const s = result.elapsedSec === 1 ? '' : 's';

      if (!result.full.trim()) {
        console.log(chalk.dim(`* Thought for ${result.elapsedSec} second${s}`));
      } else {
        console.log(
          chalk.dim(`* Thought for ${result.elapsedSec} second${s} `) +
          chalk.gray(`[ctrl+o to expand]`)
        );
        if (result.preview && event.decision !== 'STOP' && !result.preview.trim().startsWith('{')) {
          console.log(chalk.white(result.preview));
        }
      }

      this.currentThoughtId = null;
      this.pendingThoughtText = '';
    }

    if (event.decision === 'STOP') {
      const reason = event.reason || 'تم إنجاز المهمة بنجاح.';
      console.log(chalk.magenta(reason));
      
      // تفريغ ذاكرة تفاصيل الأدوات قسرياً لمنع تسرب الذاكرة (Memory Cleanup)
      this.callDetails.clear();
      
      this.rl.prompt();
    }
  }

  private handleConfirmationRequest(): void {
    console.log(chalk.dim('  └─ رفض (قم بتوجيه الوكيل بمسار مختلف وآمن)'));
    this.awaitingConfirmation = true;
    this.rl.setPrompt(chalk.red('❯ تأكيد التنفيذ؟ (y/n) '));
    this.rl.prompt();
  }

  private handleToolCall(event: any): void {
    const { toolName, callId, arguments: args = {} } = event;
    
    if (toolName === 'write_todos' || toolName === 'update_todo') return;

    let formattedDetail = '...';

    // تنظيف استخراج الحجج باستخدام Pattern Matching مبسط
    if (['execute_bash', 'run_command'].includes(toolName)) {
      formattedDetail = args.command || args.CommandLine || '...';
    } else if (['file_read', 'view_file'].includes(toolName)) {
      formattedDetail = args.path || args.AbsolutePath || '...';
    } else if (['file_glob', 'list_dir'].includes(toolName)) {
      formattedDetail = args.pattern || args.DirectoryPath || '...';
    } else if (['file_write', 'replace_file_content', 'multi_replace_file_content'].includes(toolName)) {
      formattedDetail = args.path || args.TargetFile || '...';
    } else {
      try {
        formattedDetail = JSON.stringify(args).substring(0, 40) + '...';
      } catch {
        formattedDetail = '...';
      }
    }

    this.callDetails.set(callId, formattedDetail);

    // حجب الـ Badge للأنواع التي ستعرض لاحقاً
    if (!['file_read', 'view_file', 'file_glob', 'list_dir'].includes(toolName)) {
      console.log(renderBadge(toolName, formattedDetail));
    }
  }

  private handleToolResult(event: any): void {
    const { toolName, callId, result, sessionId } = event;

    if (toolName === 'write_todos' || toolName === 'update_todo') {
      const items = todoStore.getAll(sessionId);
      console.log('\n' + chalk.bgMagenta.white.bold(' المهام المجدولة (TODOS) ') + ' ' + chalk.dim(`[${items.length} عنصر]`));
      items.forEach(item => {
        console.log(`  ${item.done ? chalk.green('☑') : chalk.white('☐')} ${item.done ? chalk.strikethrough.dim(item.text) : item.text}`);
      });
      return;
    }

    const detail = this.callDetails.get(callId) || '...';
    
    // تنظيف الذاكرة (Memory Cleanup): إزالة التفاصيل بعد الاستخدام
    this.callDetails.delete(callId);

    if (toolName === 'file_read' || toolName === 'view_file') {
      const { summary } = formatReadResult(result);
      console.log(renderBadge('file_read', detail) + ' ' + chalk.dim(summary));
      return;
    }

    if (toolName === 'file_glob' || toolName === 'list_dir') {
      const { summary, directories } = formatListResult(result);
      console.log(renderBadge('list_dir', detail) + ' ' + chalk.dim(summary));
      
      if (directories.length > 0) {
        console.log(chalk.dim('        المجلدات:'));
        directories.slice(0, 3).forEach(d => console.log(chalk.dim(`        - ${d}`)));
        
        if (directories.length > 3) {
          const hidden = directories.length - 3;
          globalExpandBuffer.push(`list-${callId}`, directories.map(d => `        - ${d}`).join('\n'));
          console.log(chalk.dim(`        ... +${hidden} أسطر أخرى `) + chalk.gray('[ctrl+o للعرض]'));
        }
      }
      return;
    }

    // المعالجة العامة للنتائج
    let summary = 'اكتملت المهمة';
    let color = chalk.green;

    if (typeof result === 'object' && result !== null) {
      if ('exitCode' in result) {
        color = result.exitCode === 0 ? chalk.green : chalk.red;
        summary = `رمز الخروج: ${result.exitCode}`;
      } else if ('lineCount' in result) {
        summary = `${result.lineCount} سطر`;
      } else if ('itemsFound' in result) {
        summary = `تم العثور على ${result.itemsFound} عنصر`;
      }
    } else if (typeof result === 'string') {
      summary = `مخرجات من ${result.split('\n').length} سطر`;
    }

    console.log(chalk.dim('  └─ ') + color(summary));
  }

  private handleProcessOutput(event: any): void {
    if (event.chunk) {
      process.stdout.write(String(event.chunk));
    } else if (event.chunks && Array.isArray(event.chunks)) {
      process.stdout.write(event.chunks.join(''));
    }
  }

  /**
   * أداة مساعدة لمنع تشوه واجهة سطر الأوامر:
   * تمسح السطر الحالي للـ prompt، وتنفذ الطابعة (log)، ثم تعيد طباعة الـ prompt.
   */
  private clearLineAndWrite(writer: () => void): void {
    readline.clearLine(process.stdout, 0);
    readline.cursorTo(process.stdout, 0);
    writer();
    // إعادة رسم الـ prompt فقط إذا لم نكن ننتظر إدخال مستخدم خاص
    if (!this.awaitingConfirmation) {
      this.rl.prompt(true);
    }
  }
}
