// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
//  NABD_OS Core — ReAct Loop Orchestrator
//  Think → Act → Observe → Repeat (max N iterations)
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

import {
  executeTool,
  parseAgentResponse,
  getNabdNuclearPrompt,
  type ToolAction,
} from './toolEngine.js';
import { loadConfig } from './configManager.js';
import { optimizeContext } from './contextOptimizer.js';

// ── Types ──────────────────────────────────────

export interface ReActCallbacks {
  /** LLM is computing — show spinner */
  onThinking:   (iteration: number) => void;
  /** Agent expressed internal reasoning */
  onThought:    (thought: string, iteration: number) => void;
  /** Tool is about to execute */
  onToolExec:   (action: string, payload: string, iteration: number) => void;
  /** Tool execution result */
  onToolResult: (result: string, iteration: number) => void;
  /** Task complete — final response to user */
  onAnswer:     (answer: string) => void;
  /** Unrecoverable error */
  onError:      (error: string) => void;
}

export interface HistoryEntry {
  role: 'user' | 'agent';
  content: string;
}

// ── Constants ──────────────────────────────────

const MAX_ITERATIONS = 12;
const MAX_PARSE_RETRIES = 3;  // consecutive JSON failures before hard stop

// ── Ollama Non-Streaming Call ──────────────────
//    ReAct requires complete JSON — streaming would
//    produce partial/invalid JSON fragments.

async function callEngine(prompt: string, signal?: AbortSignal): Promise<string | null> {
  const config = loadConfig();
  
  try {
    const res = await fetch(`${config.endpoint}/api/generate`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        model: config.model,
        prompt,
        stream: false,
        options: {
          num_predict: 768,
          temperature: 0.3,   // Low temperature → deterministic tool calls
          top_p: 0.9,
        },
      }),
      signal,
    });

    if (!res.ok) throw new Error(`HTTP ${res.status}: ${res.statusText}`);
    const data = await res.json() as { response?: string };
    return data.response?.trim() || null;
  } catch (err: any) {
    if (err.name === 'AbortError') return null;
    throw err;
  }
}

// ── ReAct Loop ─────────────────────────────────

export async function runReActLoop(
  userPrompt: string,
  history: HistoryEntry[],
  callbacks: ReActCallbacks,
  signal?: AbortSignal,
): Promise<void> {
  // Build conversation context for the LLM
  const context: string[] = [];

  // Inject recent history (last 8 exchanges max to fit context window)
  for (const entry of history.slice(-8)) {
    const label = entry.role === 'user' ? 'User' : 'Assistant';
    context.push(`${label}: ${entry.content}`);
  }

  // Current user query
  context.push(`User: ${userPrompt}`);

  let consecutiveParseFailures = 0;

  for (let iteration = 1; iteration <= MAX_ITERATIONS; iteration++) {
    if (signal?.aborted) return;

    callbacks.onThinking(iteration);

    // Optimize context window
    const optimizedContext = optimizeContext(context);

    // Assemble full prompt
    const fullPrompt = [
      getNabdNuclearPrompt(),
      '',
      '--- CONVERSATION ---',
      ...optimizedContext,
      '',
      'Assistant:',
    ].join('\n');

    // Call engine
    let raw: string | null;
    try {
      raw = await callEngine(fullPrompt, signal);
    } catch (err: any) {
      callbacks.onError(`Engine link severed: ${err.message}`);
      return;
    }

    if (signal?.aborted) return;

    if (!raw) {
      callbacks.onError('Empty response from local engine.');
      return;
    }

    // Parse structured action
    const action = parseAgentResponse(raw);

    if (!action) {
      consecutiveParseFailures++;

      if (consecutiveParseFailures >= MAX_PARSE_RETRIES) {
        callbacks.onError(
          `Model failed to produce valid JSON after ${MAX_PARSE_RETRIES} attempts. Last output: "${raw.slice(0, 200)}"`
        );
        return;
      }

      // Inject correction into context and retry
      context.push(`Assistant: ${raw}`);
      context.push(
        `System: ERROR — Your output was NOT valid JSON. You MUST respond with ONLY a JSON object: {"thought":"...","action":"...","payload":"..."}. Attempt ${consecutiveParseFailures}/${MAX_PARSE_RETRIES}.`
      );
      continue;
    }

    // Reset failure counter on successful parse
    consecutiveParseFailures = 0;

    // Emit thought if present
    if (action.thought) {
      callbacks.onThought(action.thought, iteration);
    }

    // ── Handle final_answer ──
    if (action.action === 'final_answer') {
      callbacks.onAnswer(action.payload);
      return;
    }

    // ── Execute tool ──
    callbacks.onToolExec(action.action, action.payload, iteration);

    let result: string;
    try {
      result = await executeTool(action);
    } catch (err: any) {
      result = `EXECUTION_ERROR: ${err.message}`;
    }

    if (signal?.aborted) return;

    callbacks.onToolResult(result, iteration);

    // Feed observation back into context
    context.push(`Assistant: ${JSON.stringify({ thought: action.thought, action: action.action, payload: action.payload })}`);
    context.push(`Observation: ${result}`);
  }

  // Loop exhausted
  callbacks.onError(`ReAct loop exceeded ${MAX_ITERATIONS} iterations without reaching final_answer.`);
}
