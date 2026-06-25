// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
//  CyberIndicator — Braille Spinner for Active States
//  Lightweight interval (80ms) — no GPU, no reflow cost
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

import React, { useState, useEffect } from 'react';
import { Text, Box } from 'ink';
import chalk from 'chalk';

type IndicatorStatus = 'THINKING' | 'EXECUTING' | 'ERROR';

interface CyberIndicatorProps {
  status: IndicatorStatus;
  label: string;
}

const FRAMES = ['⠋', '⠙', '⠹', '⠸', '⠼', '⠴', '⠦', '⠧', '⠇', '⠏'];

const STATUS_COLOR: Record<IndicatorStatus, (s: string) => string> = {
  THINKING:  chalk.magenta,
  EXECUTING: chalk.cyan,
  ERROR:     chalk.red,
};

export const CyberIndicator: React.FC<CyberIndicatorProps> = ({ status, label }) => {
  const [frame, setFrame] = useState(0);

  useEffect(() => {
    if (status === 'ERROR') return;
    const timer = setInterval(() => {
      setFrame(prev => (prev + 1) % FRAMES.length);
    }, 80);
    return () => clearInterval(timer);
  }, [status]);

  const colorize = STATUS_COLOR[status];
  const icon = status === 'ERROR' ? '✖' : FRAMES[frame];

  return (
    <Box>
      <Text bold>{colorize(icon)} </Text>
      <Text italic dimColor>{label}</Text>
    </Box>
  );
};
