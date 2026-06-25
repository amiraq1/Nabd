// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
//  NABD_OS Core — Tool Engine
//  Strict schemas, sandboxed execution, workspace-bounded I/O
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

import { spawn } from 'node:child_process';
import { readFile, writeFile, mkdir } from 'node:fs/promises';
import { dirname, resolve, relative } from 'node:path';
import { z } from 'zod';
import { searchSemanticMemory, insertSemanticMemory } from './vectorMemory.js';

// ── Action Schema ──────────────────────────────

export const ToolActionSchema = z.object({
  thought: z.string().optional(),
  action: z.enum(['bash', 'fs_read', 'fs_write', 'memory_search', 'memory_store', 'final_answer']),
  payload: z.string(),
});

export type ToolAction = z.infer<typeof ToolActionSchema>;

// ── Engine Constants ───────────────────────────

const MAX_OUTPUT_BYTES = 8192;
const COMMAND_TIMEOUT_MS = 30_000;
const WORKSPACE_ROOT = process.env.NABD_WORKSPACE || process.cwd();

// Patterns that must never execute — even in a local sandbox
const BLOCKED_PATTERNS: RegExp[] = [
  /\brm\s+-[rR]f\s+\/\s*$/,     // rm -rf /
  /\bmkfs\b/,                    // filesystem format
  /:\(\)\{.*\};\s*:/,            // fork bomb
  />\s*\/dev\/sd/,               // raw disk write
  /\bdd\b.*\bof=\/dev\//,       // dd to device
];

// ── Workspace Path Guard ───────────────────────

function guardPath(filePath: string): string {
  const abs = resolve(WORKSPACE_ROOT, filePath);
  const rel = relative(WORKSPACE_ROOT, abs);
  if (rel.startsWith('..') || rel.startsWith('/')) {
    throw new Error(`Path "${filePath}" escapes workspace boundary.`);
  }
  return abs;
}

// ── Tool Implementations ───────────────────────

export async function executeBash(command: string): Promise<string> {
  for (const pattern of BLOCKED_PATTERNS) {
    if (pattern.test(command)) {
      return 'BLOCKED: Command matched a dangerous pattern. Choose a safer approach.';
    }
  }

  return new Promise((resolve) => {
    let output = '';
    let truncated = false;

    const proc = spawn('bash', ['-c', command], {
      cwd: WORKSPACE_ROOT,
      env: { ...process.env, TERM: 'dumb', PAGER: 'cat' },
      timeout: COMMAND_TIMEOUT_MS,
    });

    const collect = (data: Buffer) => {
      if (truncated) return;
      output += data.toString();
      if (output.length > MAX_OUTPUT_BYTES) {
        output = output.slice(0, MAX_OUTPUT_BYTES) + '\n…[OUTPUT TRUNCATED]';
        truncated = true;
        proc.kill('SIGTERM');
      }
    };

    proc.stdout.on('data', collect);
    proc.stderr.on('data', collect);

    proc.on('error', (err) => {
      resolve(`SPAWN_ERROR: ${err.message}`);
    });

    proc.on('close', (code) => {
      resolve(output.trim() || `[Process exited with code ${code}]`);
    });
  });
}

export async function fsRead(filePath: string): Promise<string> {
  try {
    const abs = guardPath(filePath);
    const content = await readFile(abs, 'utf-8');
    if (content.length > MAX_OUTPUT_BYTES) {
      return content.slice(0, MAX_OUTPUT_BYTES) + '\n…[FILE TRUNCATED]';
    }
    return content;
  } catch (err: any) {
    return `FS_READ_ERROR: ${err.message}`;
  }
}

export async function fsWrite(payload: string): Promise<string> {
  try {
    const separatorIndex = payload.indexOf('::');
    if (separatorIndex === -1) {
      return 'FORMAT_ERROR: fs_write payload must use "path::content" format.';
    }

    const filePath = payload.slice(0, separatorIndex).trim();
    const content = payload.slice(separatorIndex + 2);
    const abs = guardPath(filePath);

    await mkdir(dirname(abs), { recursive: true });
    await writeFile(abs, content, 'utf-8');
    return `Written: ${filePath} (${content.length} bytes)`;
  } catch (err: any) {
    return `FS_WRITE_ERROR: ${err.message}`;
  }
}

// ── Unified Tool Router ────────────────────────

export async function executeTool(action: ToolAction): Promise<string> {
  switch (action.action) {
    case 'bash':         return executeBash(action.payload);
    case 'fs_read':      return fsRead(action.payload);
    case 'fs_write':     return fsWrite(action.payload);
    case 'memory_search': {
      const results = await searchSemanticMemory(action.payload);
      return results.map(r => r.text).join('\n---\n') || "No relevant memories found.";
    }
    case 'memory_store': {
      await insertSemanticMemory(action.payload);
      return "Memory successfully vectorized and archived.";
    }
    case 'final_answer': return action.payload;
  }
}

// ── Response Parser (Robust JSON Extraction) ───

export function parseAgentResponse(raw: string): ToolAction | null {
  try {
    // Extract the first JSON object, even if wrapped in markdown or filler text
    const match = raw.match(/\{[\s\S]*\}/);
    if (!match) return null;
    const parsed = JSON.parse(match[0]);
    return ToolActionSchema.parse(parsed);
  } catch {
    return null;
  }
}

// ── Nuclear System Prompt ──────────────────────

export const getNabdNuclearPrompt = () => {
  const currentDirectory = process.cwd();

  return `You are NABD_OS, a local Edge AI agent running inside Termux on Android.
CRITICAL: Your current working directory is [ ${currentDirectory} ].
Limit all filesystem and bash operations strictly to this path unless explicitly instructed otherwise.

You possess a long-term Vector Semantic Memory. Use it to recall past directives, context, or code snippets.

You operate in a strict ReAct loop: Think → Act → Observe → Repeat.

YOUR ENTIRE OUTPUT must be a single JSON object. No markdown. No extra text.

FORMAT:
{"thought":"your reasoning","action":"tool_name","payload":"argument"}

AVAILABLE TOOLS:
1. "bash" — Execute a shell command in Termux.
   Payload: the command string.
   Example: {"thought":"List files","action":"bash","payload":"ls -la"}

2. "fs_read" — Read a file from the workspace.
   Payload: relative file path.
   Example: {"thought":"Read config","action":"fs_read","payload":"package.json"}

3. "fs_write" — Create or overwrite a file.
   Payload: "relative/path::file content" (path and content separated by ::).
   Example: {"thought":"Create file","action":"fs_write","payload":"hello.txt::Hello World"}

4. "memory_search" — Search your semantic vector database using a query string. Use this if you lack context.
   Payload: your search query.
   Example: {"thought":"Find user preference","action":"memory_search","payload":"favorite color"}

5. "memory_store" — Archive crucial information, summaries, or learned rules into your long-term memory.
   Payload: the text to archive.
   Example: {"thought":"Save rule","action":"memory_store","payload":"User prefers Python over JavaScript"}

6. "final_answer" — Respond to the user when the task is COMPLETE.
   Payload: your response text.
   Example: {"thought":"Done","action":"final_answer","payload":"Task completed successfully."}

RULES:
- Output ONLY valid JSON. No markdown code fences. No commentary outside the JSON.
- Always include "thought" to explain your reasoning before acting.
- One action per response. Be atomic and focused.
- Use "final_answer" ONLY when the task is fully resolved.
- If a tool returns an error, analyze it and try a corrected approach.
- Never chain multiple commands with && unless necessary; prefer one command at a time.
- Workspace root: ${WORKSPACE_ROOT}
`;
};
