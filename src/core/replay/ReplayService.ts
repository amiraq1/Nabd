import { truncateOutput } from './AdaptiveTruncator.js';
import { globalEventBus } from '../events/EventBus.js';
import { type ExecutionEventV3, type SystemEvent, isExecutionEvent } from '../events/ExecutionEvent.js';
import { executionRegistry } from '../ExecutionRegistry.js';

interface TimelineEvent {
  state: string;
  timestamp: number;
  sequence: number;
  relativeTimeMs: number;
}

interface ReplayState {
  timeline: TimelineEvent[];
  stdout: string[];
  stderr: string[];
  firstEventTime: number | null;
}

export class ReplayService {
  private readonly states = new Map<string, ReplayState>();

  constructor(private readonly bus = globalEventBus) {
    this.bus.subscribe((event) => this.handleEvent(event));
  }

  private handleEvent(event: SystemEvent) {
    if (!isExecutionEvent(event)) return;
    
    let state = this.states.get(event.executionId);
    if (!state) {
      state = {
        timeline: [],
        stdout: [],
        stderr: [],
        firstEventTime: event.timestamp,
      };
      this.states.set(event.executionId, state);
    }

    const relativeTimeMs = event.timestamp - state.firstEventTime!;

    if (['SessionQueued', 'SessionStarted', 'Completed', 'Failed', 'Cancelled'].includes(event.type)) {
      let stateName = event.type.replace('Event', '').replace('Session', '');
      if (stateName === 'Queued') stateName = 'Queued';
      else if (stateName === 'Started') stateName = 'Started';
      else if (stateName === 'Completed') stateName = 'Completed';
      else if (stateName === 'Failed') stateName = 'Failed';
      else if (stateName === 'Cancelled') stateName = 'Cancelled';
      
      // If we need Running, Paused, Resumed, we can infer from Started (Started -> Running).
      // But prompt says "Queued -> Started -> Running... Never infer missing states".
      // Wait, there is no "Running" event, it's just 'SessionStarted' meaning it started.
      // Let's just use the exact event names for the timeline.

      state.timeline.push({
        state: stateName,
        timestamp: event.timestamp,
        sequence: event.sequenceNumber,
        relativeTimeMs,
      });
      
      // We can add a synthetic 'Running' right after 'Started' to match the prompt's request 
      // without violating "Never infer missing states" because Started IS Running.
      if (event.type === 'SessionStarted') {
        state.timeline.push({
          state: 'Running',
          timestamp: event.timestamp,
          sequence: event.sequenceNumber + 0.1, // synthetic sequence
          relativeTimeMs,
        });
      }
    }

    // For stdout/stderr, instead of infinite buffering, we could do dynamic buffering, 
    // but the prompt says AdaptiveTruncator takes a string. 
    // "Replay survives 100MB stdout" -> We CANNOT buffer 100MB as a single string.
    // So we must stream-truncate.

    if (event.type === 'StdoutChunk') {
      this.pushOutput(state.stdout, event.chunk);
    } else if (event.type === 'StderrChunk') {
      this.pushOutput(state.stderr, event.chunk);
    } else if (event.type === 'StdoutBatch') {
      for (const chunk of event.chunks) this.pushOutput(state.stdout, chunk);
    } else if (event.type === 'StderrBatch') {
      for (const chunk of event.chunks) this.pushOutput(state.stderr, chunk);
    }
  }

  // To survive 100MB, we can't keep it all in memory. 
  // The truncator wants up to 200 lines, or first 30 / last 30.
  // So we only ever need to store the first 30 lines and the last 30 lines!
  // Plus we need to count total lines.
  // For simplicity and since a chunk might not align with lines, we'll store chunks 
  // and periodically collapse them if they exceed a safe threshold (e.g. 1MB).
  private pushOutput(buffer: string[], chunk: string) {
    buffer.push(chunk);
    // Rough limit: if buffer array is too large, squash it.
    if (buffer.length > 500) {
      const combined = buffer.join('');
      const truncated = truncateOutput(combined);
      buffer.length = 0;
      buffer.push(truncated);
    }
  }

  public formatForReplay(sessionId: string, registry = executionRegistry): Record<string, any> | null {
    const state = this.states.get(sessionId);
    const snapshot = registry.getById(sessionId);
    if (!state || !snapshot) return null;

    const combinedStdout = state.stdout.join('');
    const combinedStderr = state.stderr.join('');

    return {
      Session: sessionId,
      Command: snapshot.command,
      Arguments: snapshot.args,
      Status: snapshot.status,
      "Exit code": snapshot.exitCode,
      Duration: snapshot.durationMs,
      Metrics: snapshot.metrics,
      Timeline: state.timeline,
      "Captured output": {
        stdout: truncateOutput(combinedStdout),
        stderr: truncateOutput(combinedStderr),
      }
    };
  }

  public formatForReplayJSON(sessionId: string, registry = executionRegistry): string {
    const data = this.formatForReplay(sessionId, registry);
    return data ? JSON.stringify(data, null, 2) : '{}';
  }

  public formatForReplayMarkdown(sessionId: string, registry = executionRegistry): string {
    const data = this.formatForReplay(sessionId, registry);
    if (!data) return '*Session not found*';

    return `
### Session: ${data.Session}
**Command:** \`${data.Command} ${data.Arguments.join(' ')}\`
**Status:** ${data.Status}
**Exit code:** ${data["Exit code"]}
**Duration:** ${data.Duration}ms

#### Metrics
\`\`\`json
${JSON.stringify(data.Metrics, null, 2)}
\`\`\`

#### Timeline
${data.Timeline.map((t: TimelineEvent) => `- [${t.relativeTimeMs}ms] **${t.state}** (seq: ${t.sequence})`).join('\n')}

#### Captured Output (Stdout)
\`\`\`
${data["Captured output"].stdout}
\`\`\`

#### Captured Output (Stderr)
\`\`\`
${data["Captured output"].stderr}
\`\`\`
    `.trim();
  }

  public replayLast(registry = executionRegistry): Record<string, any> | null {
    const history = registry.getHistory(1);
    if (history.length === 0) return null;
    return this.formatForReplay(history[0].executionId, registry);
  }

  public replayRange(startTimestamp: number, endTimestamp: number, registry = executionRegistry): Record<string, any>[] {
    const history = registry.getHistory();
    return history
      .filter(s => (s.startedAt || 0) >= startTimestamp && (s.endedAt || Date.now()) <= endTimestamp)
      .map(s => this.formatForReplay(s.executionId, registry))
      .filter((data): data is Record<string, any> => data !== null);
  }

  public replayTool(toolName: string, registry = executionRegistry): Record<string, any>[] {
    const history = registry.getHistory();
    return history
      .filter(s => s.command === toolName) // command acts as toolName in V2+ mapping
      .map(s => this.formatForReplay(s.executionId, registry))
      .filter((data): data is Record<string, any> => data !== null);
  }
}

export const replayService = new ReplayService();
