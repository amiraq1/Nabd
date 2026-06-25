// core/contextOptimizer.ts

export interface OptimizeOptions {
  maxReActIterationsToKeep?: number;
  maxToolResultLength?: number;
}

/**
 * Optimizes the ReAct loop context by truncating long observations
 * and removing older ReAct iterations to prevent context window overflow.
 */
export const optimizeContext = (
  context: string[],
  options: OptimizeOptions = { maxReActIterationsToKeep: 4, maxToolResultLength: 1500 }
): string[] => {
  const { maxReActIterationsToKeep = 4, maxToolResultLength = 1500 } = options;
  
  if (context.length <= 4) return context;

  // We want to keep all original User/Assistant history (which usually comes first),
  // but prune the dynamic "Assistant:" and "Observation:" pairs added during the ReAct loop.
  
  const optimized: string[] = [];
  let recentIterations: string[] = [];
  
  // Truncate long observations and collect them
  const processedContext = context.map(line => {
    if (line.startsWith('Observation: ') && line.length > maxToolResultLength) {
      return line.substring(0, maxToolResultLength) + '\n... [TRUNCATED TO PRESERVE CONTEXT WINDOW]';
    }
    return line;
  });

  // A basic heuristic to keep history + only the last N iterations of ReAct
  // Iterations typically come in pairs or triplets (Assistant tool call -> Observation -> System error)
  // We'll just slice the array safely. We keep the first few lines (user history) intact.
  
  // Find where the ReAct loop actually started (where the first Observation or Assistant tool call happened)
  // If we can't reliably detect, we just keep the base history and the last few messages.
  const historyBoundary = processedContext.findIndex(line => line.startsWith('Observation:')) - 1;
  
  if (historyBoundary > 0) {
    const history = processedContext.slice(0, historyBoundary);
    const loopLogs = processedContext.slice(historyBoundary);
    
    // Each iteration is roughly 2-3 lines (Assistant + Observation + optional System)
    // We'll keep the last N * 3 lines of the loop logs
    const linesToKeep = maxReActIterationsToKeep * 3;
    const prunedLogs = loopLogs.slice(-linesToKeep);
    
    return [...history, ...prunedLogs];
  }

  // Fallback if no observations exist yet
  return processedContext;
};
