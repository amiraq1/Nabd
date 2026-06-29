import type { PermissionLevel } from '../types.js';

export type AgentRole = 
  | 'READ_ONLY_AGENT'
  | 'BUILDER_AGENT'
  | 'SYSTEM_AGENT'
  | 'ROOT_AGENT';

export interface SecurityContext {
  role: AgentRole;
  permissions: PermissionLevel[];
  sessionId: string;
  workspaceRoot: string;
  networkPolicy: 'allow' | 'deny';
  filesystemPolicy: 'read_only' | 'read_write' | 'none';
  createdAt: number;
}
