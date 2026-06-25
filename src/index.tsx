#!/usr/bin/env node
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
//  NABD_OS v0.4.0 — Agentic Edge AI Dashboard
//  Bento-Grid TUI with ReAct Loop + Tool Execution
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

import React, { useState, useRef, useCallback } from 'react';
import { render, Box, Text, useInput, useStdout } from 'ink';
import chalk from 'chalk';
import { executeTermuxCommand } from './core/toolEngine.js';
import { runReActLoop, type HistoryEntry } from './core/reactLoop.js';
import { loadConfig } from './core/configManager.js';
import { CyberIndicator } from './components/CyberIndicator.js';

// ── Types ──────────────────────────────────────

type MessageRole = 'user' | 'agent' | 'system' | 'thought' | 'tool_call' | 'tool_result';

interface Message {
  id: string;
  role: MessageRole;
  content: string;
  timestamp: number;
  meta?: {
    action?: string;
    iteration?: number;
  };
}

interface AgentState {
  status: 'IDLE' | 'THINKING' | 'EXECUTING' | 'ERROR';
  currentTask: string;
  iteration: number;
  totalSteps: number;
}

// ── Helpers ────────────────────────────────────

let msgCounter = 0;
const uid = (prefix: string) => `${prefix}-${++msgCounter}-${Date.now()}`;

// ── Visual Components ──────────────────────────

const BentoBox = ({ children, title, width, height, borderColor = 'cyan' }: {
  children: React.ReactNode;
  title?: string;
  width?: number | string;
  height?: number | string;
  borderColor?: string;
}) => (
  <Box
    borderStyle="round"
    borderColor={borderColor}
    width={width}
    height={height}
    paddingX={1}
    flexDirection="column"
  >
    {title && (
      <Box marginTop={-1} marginLeft={1} paddingX={1}>
        <Text bold color={borderColor}>{title}</Text>
      </Box>
    )}
    {children}
  </Box>
);

const StatusBadge = ({ status }: { status: AgentState['status'] }) => {
  const cfg = {
    IDLE:      { color: 'green'   as const, icon: '●', label: 'STANDBY' },
    THINKING:  { color: 'yellow'  as const, icon: '◉', label: 'THINKING' },
    EXECUTING: { color: 'magenta' as const, icon: '⚡', label: 'EXECUTING' },
    ERROR:     { color: 'red'     as const, icon: '✖', label: 'ERROR' },
  }[status];
  return <Text bold color={cfg.color}>{cfg.icon} {cfg.label}</Text>;
};

// Role-based message renderer
const MessageLine = ({ msg }: { msg: Message }) => {
  switch (msg.role) {
    case 'user':
      return (
        <Box>
          <Text color="green" bold>USR&gt; </Text>
          <Text wrap="wrap">{msg.content}</Text>
        </Box>
      );

    case 'thought':
      return (
        <Box paddingLeft={2} borderStyle="single" borderLeft borderRight={false} borderTop={false} borderBottom={false} borderColor="magenta">
          <CyberIndicator status="THINKING" label={msg.content} />
        </Box>
      );

    case 'tool_call':
      return (
        <Box paddingLeft={2} borderStyle="single" borderLeft borderRight={false} borderTop={false} borderBottom={false} borderColor="cyan">
          <Text dimColor>[{msg.meta?.iteration}] </Text>
          <CyberIndicator status="EXECUTING" label={`${msg.meta?.action}: ${msg.content.length > 50 ? msg.content.slice(0, 50) + '…' : msg.content}`} />
        </Box>
      );

    case 'tool_result': {
      const lines = msg.content.split('\n');
      const preview = lines.slice(0, 4);
      const hasMore = lines.length > 4;
      return (
        <Box flexDirection="column" paddingLeft={5}>
          {preview.map((line, i) => (
            <Text key={i} dimColor wrap="wrap">{line}</Text>
          ))}
          {hasMore && <Text dimColor>  …[{lines.length - 4} more lines]</Text>}
        </Box>
      );
    }

    case 'agent':
      return (
        <Box flexDirection="column">
          <Box>
            <Text color="magenta" bold>LLM&gt; </Text>
            <Text wrap="wrap">{msg.content.split('\n')[0]}</Text>
          </Box>
          {msg.content.split('\n').slice(1).map((line, i) => (
            <Box key={i} paddingLeft={5}>
              <Text wrap="wrap">{line}</Text>
            </Box>
          ))}
        </Box>
      );

    case 'system':
      return (
        <Box>
          <Text color="red" bold dimColor>SYS&gt; </Text>
          <Text dimColor wrap="wrap">{msg.content}</Text>
        </Box>
      );

    default:
      return <Text>{msg.content}</Text>;
  }
};

// ── Core Dashboard ─────────────────────────────

const AgentDashboard = () => {
  const { stdout } = useStdout();
  const termHeight = stdout?.rows ?? 24;
  const termWidth  = stdout?.columns ?? 80;

  const [input, setInput] = useState('');
  const [messages, setMessages] = useState<Message[]>([]);
  const [scrollOffset, setScrollOffset] = useState(0);
  const [agentState, setAgentState] = useState<AgentState>({
    status: 'IDLE',
    currentTask: 'Awaiting directive...',
    iteration: 0,
    totalSteps: 0,
  });

  const abortRef = useRef<AbortController | null>(null);

  // ── Push message helper ────────────────────

  const pushMsg = useCallback((role: MessageRole, content: string, meta?: Message['meta']) => {
    setMessages(prev => [...prev, {
      id: uid(role),
      role,
      content,
      timestamp: Date.now(),
      meta,
    }]);
    setScrollOffset(0); // Gravity: snap to bottom
  }, []);

  // ── ReAct Loop Launcher ────────────────────

  const launchReAct = useCallback(async (prompt: string) => {
    // Abort any running loop
    if (abortRef.current) abortRef.current.abort();
    const controller = new AbortController();
    abortRef.current = controller;

    // Build history from previous user/agent messages
    const history: HistoryEntry[] = messages
      .filter(m => m.role === 'user' || m.role === 'agent')
      .slice(-8)
      .map(m => ({ role: m.role as 'user' | 'agent', content: m.content }));

    setAgentState({
      status: 'THINKING',
      currentTask: 'Initializing ReAct loop...',
      iteration: 0,
      totalSteps: 0,
    });

    await runReActLoop(prompt, history, {
      onThinking: (iter) => {
        setAgentState(prev => ({
          ...prev,
          status: 'THINKING',
          currentTask: `ReAct iteration ${iter}...`,
          iteration: iter,
        }));
      },

      onThought: (thought, iter) => {
        pushMsg('thought', thought, { iteration: iter });
      },

      onToolExec: (action, payload, iter) => {
        setAgentState(prev => ({
          ...prev,
          status: 'EXECUTING',
          currentTask: `${action}: ${payload.slice(0, 40)}`,
          iteration: iter,
        }));
        pushMsg('tool_call', payload, { action, iteration: iter });
      },

      onToolResult: (result, iter) => {
        pushMsg('tool_result', result, { iteration: iter });
        setAgentState(prev => ({
          ...prev,
          totalSteps: prev.totalSteps + 1,
        }));
      },

      onAnswer: (answer) => {
        pushMsg('agent', answer);
        setAgentState(prev => ({
          ...prev,
          status: 'IDLE',
          currentTask: 'Awaiting directive...',
          totalSteps: prev.totalSteps + 1,
        }));
      },

      onError: (error) => {
        pushMsg('system', error);
        setAgentState(prev => ({
          ...prev,
          status: 'ERROR',
          currentTask: 'Neural link severed',
        }));
      },
    }, controller.signal);
  }, [messages, pushMsg]);

  // ── Input Routing ──────────────────────────

  const isActive = agentState.status === 'THINKING' || agentState.status === 'EXECUTING';

  useInput((char: string, key: Record<string, boolean>) => {
    // Ctrl+C: abort
    if (char === 'c' && key.ctrl) {
      if (isActive && abortRef.current) {
        abortRef.current.abort();
        abortRef.current = null;
        pushMsg('system', 'ReAct loop aborted by operator.');
        setAgentState(prev => ({ ...prev, status: 'IDLE', currentTask: 'Aborted' }));
      }
      return;
    }

    // Viewport scroll
    if (key.upArrow) {
      setScrollOffset(prev => Math.min(prev + 1, Math.max(0, messages.length - 1)));
      return;
    }
    if (key.downArrow) {
      setScrollOffset(prev => Math.max(0, prev - 1));
      return;
    }

    // Submit
    if (key.return) {
      if (!input.trim() || isActive) return;
      const prompt = input.trim();
      pushMsg('user', prompt);
      setInput('');
      launchReAct(prompt);
      return;
    }

    // Backspace
    if (key.backspace || key.delete) {
      setInput(prev => prev.slice(0, -1));
      return;
    }

    // Character
    if (char && !key.ctrl && !key.meta) {
      setInput(prev => prev + char);
    }
  });

  // ── Virtual Viewport ──────────────────────

  const chatAreaHeight = Math.max(4, termHeight - 10);
  const MAX_VISIBLE = Math.max(3, chatAreaHeight - 2);
  const total = messages.length;
  const endIdx = Math.max(0, total - scrollOffset);
  const startIdx = Math.max(0, endIdx - MAX_VISIBLE);
  const visible = messages.slice(startIdx, endIdx);

  const isLive = scrollOffset === 0;
  const isNarrow = termWidth < 60;

  // ── Theme ─────────────────────────────────

  const T = {
    primary:   chalk.hex('#00FFD1'),
    secondary: chalk.hex('#FF00E4'),
    dim:       chalk.hex('#555555'),
  };

  const config = loadConfig();
  const modelName = config.model;
  
  // Extract port from endpoint safely
  let portStr = ':11434';
  try {
    const url = new URL(config.endpoint);
    portStr = `:${url.port || '80'}`;
  } catch (e) {
    portStr = config.endpoint.slice(-6);
  }

  // ── Render ────────────────────────────────

  return (
    <Box flexDirection="column" width="100%" height={termHeight} padding={0}>

      {/* Header */}
      <Box justifyContent="space-between" paddingX={1}>
        <Text bold>
          {T.primary('NABD')}{T.dim('_OS')} <Text dimColor>v0.4.0</Text>
        </Text>
        <Box gap={2}>
          <StatusBadge status={agentState.status} />
          {agentState.iteration > 0 && isActive && (
            <Text color="yellow" dimColor>iter:{agentState.iteration}</Text>
          )}
        </Box>
      </Box>

      {/* Main Grid */}
      <Box flexDirection="row" flexGrow={1}>

        {/* Chat Viewport */}
        <BentoBox
          title={isLive ? '◉ LIVE' : `◇ HISTORY [-${scrollOffset}]`}
          width={isNarrow ? '100%' : '72%'}
          borderColor={isLive ? 'cyan' : 'yellow'}
        >
          <Box flexDirection="column" flexGrow={1} justifyContent="flex-end" overflow="hidden">
            {visible.length === 0 ? (
              <Box flexDirection="column" justifyContent="center" alignItems="center" flexGrow={1}>
                <Text dimColor>─── ReAct Engine Online ───</Text>
                <Text dimColor italic>Type a directive. Agent will Think → Act → Observe.</Text>
              </Box>
            ) : (
              visible.map(msg => <MessageLine key={msg.id} msg={msg} />)
            )}
            {total > MAX_VISIBLE && (
              <Box justifyContent="flex-end">
                <Text dimColor>[{startIdx + 1}..{endIdx}/{total}]</Text>
              </Box>
            )}
          </Box>
        </BentoBox>

        {/* Side Panel */}
        {!isNarrow && (
          <Box flexDirection="column" width="28%">
            <BentoBox title="Telemetry" borderColor="gray">
              <Text>Engine  <Text color="cyan">{modelName.length > 8 ? modelName.slice(0, 8) + '…' : modelName}</Text></Text>
              <Text>API     <Text color="cyan">{portStr}</Text></Text>
              <Text>Steps   <Text color="yellow">{agentState.totalSteps}</Text></Text>
              <Text>Iter    <Text color="yellow">{agentState.iteration || '—'}</Text></Text>
              <Box marginTop={1}>
                <Text dimColor>──────────────</Text>
              </Box>
              <Text dimColor>↑↓  scroll</Text>
              <Text dimColor>^C  abort loop</Text>
              <Text dimColor>RET submit</Text>
            </BentoBox>

            <BentoBox title="Tools" borderColor="gray">
              <Text color="green">✓ <Text dimColor>bash</Text></Text>
              <Text color="green">✓ <Text dimColor>fs_read</Text></Text>
              <Text color="green">✓ <Text dimColor>fs_write</Text></Text>
              <Text color="green">✓ <Text dimColor>memory_search</Text></Text>
              <Text color="green">✓ <Text dimColor>memory_store</Text></Text>
              <Text color="green">✓ <Text dimColor>final_answer</Text></Text>
            </BentoBox>
          </Box>
        )}
      </Box>

      {/* Input */}
      <Box paddingX={1} flexDirection="column">
        <Box>
          <Text bold color={isActive ? 'yellow' : 'cyan'}>
            {isActive ? '⟳ ' : '~ $ '}
          </Text>
          <Text>{input}</Text>
          <Text color={isActive ? 'yellow' : 'cyan'}>█</Text>
        </Box>
        <Box justifyContent="space-between">
          <Text dimColor>{isActive ? `ReAct iter ${agentState.iteration}...` : 'ENTER to execute'}</Text>
          <Text dimColor italic>{agentState.currentTask}</Text>
        </Box>
      </Box>
    </Box>
  );
};

// ── Bootstrap ──────────────────────────────────

render(<AgentDashboard />);
