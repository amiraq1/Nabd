import readline from 'node:readline';
import { globalEventBus } from '../core/events/EventBus.js';
import { executionRegistry } from '../core/ExecutionRegistry.js';
import type { SystemEvent } from '../core/events/ExecutionEvent.js';
import { AgentLoop } from '../core/agent/AgentLoop.js';
import { semanticMemory } from '../core/memory/SemanticMemory.js';

// ANSI Escapes for Cyberpunk Neon styling
const ESC = '\x1b[';
const COLORS = {
  NEON_CYAN: `${ESC}38;2;0;255;255m`,
  NEON_PINK: `${ESC}38;2;255;0;255m`,
  NEON_GREEN: `${ESC}38;2;57;255;20m`,
  NEON_YELLOW: `${ESC}38;2;255;255;0m`,
  DARK_GRAY: `${ESC}38;2;50;50;50m`,
  RESET: `${ESC}0m`,
  CLEAR_SCREEN: `${ESC}2J${ESC}H`,
};

export class NabdCLI {
  private rl: readline.Interface;
  private agentStatus = 'IDLE';
  private plannerAction = 'WAITING';
  private latestInference = '';
  private memoryCount = 0;
  private isGenerating = false;

  constructor(private agentLoop: AgentLoop) {
    this.rl = readline.createInterface({
      input: process.stdin,
      output: process.stdout,
      prompt: `${COLORS.NEON_PINK}NABD_OS_v3 > ${COLORS.RESET}`,
    });

    this.subscribeEvents();
  }

  private subscribeEvents() {
    globalEventBus.subscribe((event: SystemEvent) => {
      let renderNeeded = false;
      
      if (event.type === 'StateTransition') {
        this.agentStatus = event.to;
        renderNeeded = true;
      }
      if (event.type === 'PlannerDecision') {
        this.plannerAction = event.decision;
        renderNeeded = true;
      }
      if (event.type === 'Token') {
        this.latestInference += event.text;
        // Keep only last 100 chars to avoid breaking layout
        if (this.latestInference.length > 100) {
          this.latestInference = '...' + this.latestInference.slice(-97);
        }
        renderNeeded = true;
      }
      if (event.type === 'InferenceStarted') {
        this.isGenerating = true;
        this.latestInference = '';
        renderNeeded = true;
      }
      if (event.type === 'Completed' || event.type === 'Cancelled' || event.type === 'Failed' || event.type === 'Timeout') {
        if ('fullText' in event || 'error' in event) { // Basic inference event check
            this.isGenerating = false;
            renderNeeded = true;
        }
      }

      if (renderNeeded && !this.rl.line) {
        this.render();
      }
    });
  }

  private drawBox(title: string, content: string, width: number, color: string): string {
    const top = `╭─ ${title} ${'─'.repeat(Math.max(0, width - title.length - 4))}╮`;
    const bottom = `╰${'─'.repeat(width - 2)}╯`;
    
    // Split content by length
    const maxLine = width - 4;
    const lines = [];
    let current = content;
    while (current.length > 0) {
      lines.push(current.slice(0, maxLine));
      current = current.slice(maxLine);
    }
    if (lines.length === 0) lines.push('');

    const middle = lines.map(l => `│ ${l.padEnd(maxLine, ' ')} │`).join('\n');
    return `${color}${top}\n${middle}\n${bottom}${COLORS.RESET}`;
  }

  private render() {
    // Cyberpunk Bento Box Layout
    process.stdout.write(COLORS.CLEAR_SCREEN);
    
    console.log(`${COLORS.NEON_CYAN}███╗   ██╗ █████╗ ██████╗ ██████╗       ██████╗ ███████╗${COLORS.RESET}`);
    console.log(`${COLORS.NEON_CYAN}████╗  ██║██╔══██╗██╔══██╗██╔══██╗     ██╔═══██╗██╔════╝${COLORS.RESET}`);
    console.log(`${COLORS.NEON_CYAN}██╔██╗ ██║███████║██████╔╝██║  ██║     ██║   ██║███████╗${COLORS.RESET}`);
    console.log(`${COLORS.NEON_CYAN}██║╚██╗██║██╔══██║██╔══██╗██║  ██║     ██║   ██║╚════██║${COLORS.RESET}`);
    console.log(`${COLORS.NEON_CYAN}██║ ╚████║██║  ██║██████╔╝██████╔╝     ╚██████╔╝███████║${COLORS.RESET}`);
    console.log(`${COLORS.NEON_CYAN}╚═╝  ╚═══╝╚═╝  ╚═╝╚═════╝ ╚═════╝       ╚═════╝ ╚══════╝${COLORS.RESET}`);
    console.log('');

    const stats = executionRegistry.stats();
    const waiting = stats.total - stats.running - stats.completed - stats.failed;
    this.memoryCount = (semanticMemory as any).entries?.length || 0;

    const boxWidth = 60;
    console.log(this.drawBox('SYSTEM STATE', `Agent: ${this.agentStatus}`, boxWidth, COLORS.NEON_CYAN));
    console.log(this.drawBox('ORCHESTRATION', `Planner: ${this.plannerAction} | Memory Items: ${this.memoryCount}`, boxWidth, COLORS.NEON_PINK));
    console.log(this.drawBox('EXECUTION QUEUE', `Waiting: ${Math.max(0, waiting)} | Running: ${stats.running}`, boxWidth, COLORS.NEON_YELLOW));
    
    if (this.isGenerating || this.latestInference) {
      console.log(this.drawBox('INFERENCE STREAM', this.latestInference.replace(/\n/g, ' '), boxWidth, COLORS.NEON_GREEN));
    }
    
    console.log('');
    this.rl.prompt(true);
  }

  public start() {
    this.render();
    this.rl.on('line', async (line) => {
      const input = line.trim();
      if (!input) {
        this.rl.prompt();
        return;
      }
      
      if (input === '/exit') {
        console.log(`${COLORS.NEON_PINK}Terminating NABD_OS... Goodbye.${COLORS.RESET}`);
        process.exit(0);
      }

      semanticMemory.remember(input, ['user-prompt']);
      
      try {
        await this.agentLoop.run(input);
      } catch (err: any) {
        console.error(`${COLORS.NEON_PINK}CRITICAL ERROR: ${err.message}${COLORS.RESET}`);
      }
      
      this.rl.prompt();
    });
  }
}
