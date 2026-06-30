import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';
import { EventBus, globalEventBus } from '../events/EventBus.js';
import { type ExecutionEventV3, type SystemEvent, isExecutionEvent } from '../events/ExecutionEvent.js';
import { ExecutionRegistry, executionRegistry } from '../ExecutionRegistry.js';

interface LoggerState {
  stdoutHash: crypto.Hash;
  stderrHash: crypto.Hash;
}

export class ExecutionLogger {
  private stream: fs.WriteStream;
  private readonly states = new Map<string, LoggerState>();
  
  // الحد الأقصى لحجم ملف السجلات (مثلاً 5 ميجابايت) قبل التدوير
  private readonly MAX_LOG_SIZE = 5 * 1024 * 1024;
  private bytesWritten = 0;

  constructor(
    private readonly logFilePath: string,
    private readonly bus = globalEventBus,
    private readonly registry = executionRegistry
  ) {
    this.ensureDirectory();
    this.stream = this.openStream();
    this.checkInitialSize();
    
    this.bus.subscribe((event) => this.handleEvent(event));

    const onShutdown = () => this.stream.end();
    process.on('beforeExit', onShutdown);
    process.on('SIGINT', onShutdown);
    process.on('SIGTERM', onShutdown);
  }

  private ensureDirectory(): void {
    const dir = path.dirname(this.logFilePath);
    if (!fs.existsSync(dir)) fs.mkdirSync(dir, { recursive: true });
  }

  private openStream(): fs.WriteStream {
    return fs.createWriteStream(this.logFilePath, { flags: 'a', encoding: 'utf8' });
  }

  private checkInitialSize(): void {
    if (fs.existsSync(this.logFilePath)) {
      this.bytesWritten = fs.statSync(this.logFilePath).size;
      this.rotateLogIfNeeded();
    }
  }

  /**
   * تدوير السجلات (Log Rotation) لمنع استنزاف مساحة تخزين الهاتف
   */
  private rotateLogIfNeeded(): void {
    if (this.bytesWritten >= this.MAX_LOG_SIZE) {
      this.stream.end();
      const backupPath = `${this.logFilePath}.${Date.now()}.bak`;
      fs.renameSync(this.logFilePath, backupPath);
      
      this.stream = this.openStream();
      this.bytesWritten = 0;
      
      // اختياري: يمكن إضافة كود هنا لحذف النسخ الاحتياطية الأقدم من 3 أيام مثلاً
    }
  }

  private handleEvent(event: SystemEvent): void {
    if (!isExecutionEvent(event)) return;

    if (event.type === 'SessionQueued') {
      this.states.set(event.executionId, {
        stdoutHash: crypto.createHash('sha256'),
        stderrHash: crypto.createHash('sha256'),
      });
      return;
    }

    const state = this.states.get(event.executionId);
    if (!state) return;

    if (event.type === 'StdoutChunk') state.stdoutHash.update(event.chunk);
    else if (event.type === 'StderrChunk') state.stderrHash.update(event.chunk);
    else if (event.type === 'StdoutBatch') event.chunks.forEach(c => state.stdoutHash.update(c));
    else if (event.type === 'StderrBatch') event.chunks.forEach(c => state.stderrHash.update(c));
    else if (['Completed', 'Failed', 'Cancelled'].includes(event.type)) {
      this.writeTerminalLog(event);
      this.states.delete(event.executionId);
    }
  }

  private writeTerminalLog(event: ExecutionEventV3) {
    const state = this.states.get(event.executionId);
    const snapshot = this.registry.getById(event.executionId);
    if (!state || !snapshot) return;

    const record = JSON.stringify({
      sessionId: event.executionId,
      command: snapshot.command,
      exitCode: snapshot.exitCode,
      durationMs: snapshot.durationMs ?? 0,
      outputBytes: snapshot.totalBytes,
      stdoutHash: state.stdoutHash.digest('hex'),
    }) + '\n';

    this.stream.write(record);
    this.bytesWritten += Buffer.byteLength(record, 'utf8');
    
    // فحص حجم الملف بعد الكتابة لتدويره إذا لزم الأمر
    this.rotateLogIfNeeded();
  }
}
