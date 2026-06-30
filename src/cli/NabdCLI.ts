import readline from 'readline';
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
  private rl: readline.Interface;
  private currentThoughtId: string | null = null;
  private thoughtTracker = new ThoughtTracker();
  private pendingThoughtText: string = '';
  private awaitingConfirmation: boolean = false;
  private callDetails = new Map<string, string>();

  constructor(private agentLoop: AgentLoop) {
    this.rl = readline.createInterface({
      input: process.stdin,
      output: process.stdout,
      terminal: true
    });
    this.rl.setPrompt(chalk.blue('> '));

    // Handle ctrl+o keypress
    process.stdin.on('keypress', (str, key) => {
      if (key && key.ctrl && key.name === 'o') {
        const lastText = this.thoughtTracker.getLastThought();
        if (lastText) {
          console.log(chalk.dim('\n* Expanded Content:'));
          console.log(chalk.gray(lastText));
          this.rl.prompt();
        }
      } else if (key && key.ctrl && key.name === 'c') {
        if (this.awaitingConfirmation) {
          this.awaitingConfirmation = false;
          this.agentLoop.confirmCommand(false);
        } else {
          process.exit(0);
        }
      }
    });

    this.setupEvents();
  }

  private setupEvents() {
    globalEventBus.subscribe((event: SystemEvent) => {
      if (event.type === 'InferenceStarted') {
        const ts = Date.now();
        if (!this.currentThoughtId) {
          this.currentThoughtId = `thought-${ts}`;
          this.thoughtTracker.start();
          this.pendingThoughtText = '';
        }
      }

      if (event.type === 'Token') {
        this.pendingThoughtText += (event as any).text || '';
      }

      if (event.type === 'Completed' && !('executionId' in event)) {
        this.pendingThoughtText = (event as any).fullText || this.pendingThoughtText;
      }

      if (event.type === 'PlannerDecision') {
        if (this.currentThoughtId) {
          const result = this.thoughtTracker.finish(this.pendingThoughtText);
          const s = result.elapsedSec === 1 ? '' : 's';
          
          if (!result.full.trim()) {
            console.log(chalk.dim(`* Thought for ${result.elapsedSec} second${s}`));
          } else {
            console.log(
              chalk.dim(`* Thought for ${result.elapsedSec} second${s}`) +
              chalk.gray(`  [ctrl+o to expand]`)
            );
            if (result.preview && (event as any).decision !== 'STOP' && result.preview.trim()[0] !== "{") {
              console.log(chalk.white(result.preview));
            }
          }
          
          this.currentThoughtId = null;
          this.pendingThoughtText = '';
        }
        
        if ((event as any).decision === 'STOP') {
          const reason = (event as any).reason || 'Task complete.';
          console.log(chalk.magenta(reason));
          this.rl.prompt();
        }
      }

      if (event.type === 'ConfirmationRequired') {
        // ToolCall has already printed the badge
        console.log(chalk.dim('  └─ No (tell Command Code what to do differently)'));
        this.awaitingConfirmation = true;
        this.rl.setPrompt(chalk.blue('> yes '));
        this.rl.prompt();
      }

      if (event.type === 'ToolCall') {
        const toolName = (event as any).toolName;
        const callId = (event as any).callId;
        if (toolName === 'write_todos' || toolName === 'update_todo') {
          return;
        }

        const args = (event as any).arguments || {};
        let formattedDetail = 'args...';
        
        if (toolName === 'execute_bash' || toolName === 'run_command') {
          formattedDetail = args.command || args.CommandLine || '...';
        } else if (toolName === 'file_read' || toolName === 'view_file') {
          formattedDetail = args.path || args.AbsolutePath || '...';
        } else if (toolName === 'file_glob' || toolName === 'list_dir') {
          formattedDetail = args.pattern || args.DirectoryPath || '...';
        } else if (toolName === 'file_write' || toolName === 'replace_file_content' || toolName === 'multi_replace_file_content' || toolName === 'write_to_file') {
          formattedDetail = args.path || args.TargetFile || '...';
        } else {
          try {
            formattedDetail = JSON.stringify(args).substring(0, 40) + '...';
          } catch {
            formattedDetail = '...';
          }
        }
        
        this.callDetails.set(callId, formattedDetail);

        if (toolName === 'file_read' || toolName === 'view_file' || toolName === 'file_glob' || toolName === 'list_dir') {
          return; // Suppress badge here, print in ToolResult
        }

        console.log(renderBadge(toolName, formattedDetail));
      }
      
      if (event.type === 'ToolResult') {
        const toolName = (event as any).toolName;
        const callId = (event as any).callId;

        if (toolName === 'write_todos' || toolName === 'update_todo') {
          const sessionId = (event as any).sessionId;
          const items = todoStore.getAll(sessionId);
          
          console.log('\n' + chalk.bgMagenta.white.bold(' TODOS ') + ' ' + chalk.dim(`[${items.length} items]`));
          items.forEach(item => {
            console.log(
              '  ' + (item.done ? chalk.green('☑') : chalk.white('☐')) +
              ' ' + (item.done ? chalk.strikethrough.dim(item.text) : item.text)
            );
          });
          return;
        }

        const detail = this.callDetails.get(callId) || '...';
        const result = (event as any).result;

        if (toolName === 'file_read' || toolName === 'view_file') {
          const { summary } = formatReadResult(result);
          console.log(renderBadge('file_read', detail) + ' ' + chalk.dim(summary));
          return;
        }

        if (toolName === 'file_glob' || toolName === 'list_dir') {
          const { summary, directories } = formatListResult(result);
          console.log(renderBadge('list_dir', detail) + ' ' + chalk.dim(summary));
          if (directories.length > 0) {
            console.log(chalk.dim('        Directories:'));
            const previewDirs = directories.slice(0, 3);
            previewDirs.forEach(d => console.log(chalk.dim(`        - ${d}`)));
            if (directories.length > 3) {
              const hidden = directories.length - 3;
              const fullText = directories.map(d => `        - ${d}`).join('\n');
              globalExpandBuffer.push(`list-${callId}`, fullText);
              console.log(chalk.dim(`        ... +${hidden} lines `) + chalk.gray('[ctrl+o to expand]'));
            }
          }
          return;
        }
        let summary = 'Finished';
        let color = chalk.green;
        
        if (typeof result === 'object' && result !== null) {
           if ('exitCode' in result) {
             const exitCode = result.exitCode;
             color = exitCode === 0 ? chalk.green : chalk.red;
             summary = `Exit ${exitCode}`;
           } else if ('lineCount' in result) {
             summary = `${result.lineCount} lines`;
           } else if ('itemsFound' in result) {
             summary = `Found ${result.itemsFound} items`;
           }
        } else if (typeof result === 'string') {
           const lines = result.split('\n').length;
           summary = `${lines} lines output`;
        }

        console.log(chalk.dim('  └─ ') + color(summary));
      }
      
      if (event.type === 'StdoutChunk' || event.type === 'StderrChunk') {
        const chunk = (event as any).chunk;
        process.stdout.write(String(chunk));
      }

      if (event.type === 'StdoutBatch' || event.type === 'StderrBatch') {
        const chunks = (event as any).chunks;
        if (Array.isArray(chunks)) {
          process.stdout.write(chunks.join(''));
        }
      }

      if (event.type === 'Failed') {
        console.log(chalk.red(`\n[Failed: ${(event as any).error}]`));
      }
    });

    this.rl.on('line', async (line) => {
      if (this.awaitingConfirmation) {
        this.awaitingConfirmation = false;
        this.rl.setPrompt(chalk.blue('> ')); // reset prompt
        const answer = line.trim().toLowerCase();
        if (answer === 'yes' || answer === 'y') {
          this.agentLoop.confirmCommand(true);
        } else {
          this.agentLoop.confirmCommand(false);
        }
        return;
      }

      const input = line.trim();
      if (!input) {
        this.rl.prompt();
        return;
      }

      if (input === '/exit') {
        process.exit(0);
      }

      try {
        await this.agentLoop.run(input);
      } catch (err) {
        console.log(chalk.red('Error: ') + (err as Error).message);
        this.rl.prompt();
      }
    });

    this.rl.on('SIGINT', () => {
      if (this.awaitingConfirmation) {
        this.awaitingConfirmation = false;
        this.rl.setPrompt(chalk.blue('> '));
        this.agentLoop.confirmCommand(false);
      } else {
        process.exit(0);
      }
    });
  }

  public start() {
    this.rl.prompt();
  }
}
