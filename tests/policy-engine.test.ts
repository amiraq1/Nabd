import { describe, it } from 'node:test';
import assert from 'node:assert';
import { PolicyEngine } from '../src/core/PolicyEngine.ts';
import type { ExecutionPolicy } from '../src/core/types.ts';

describe('PolicyEngine', () => {
  describe('default policy merge', () => {
    it('produces a fully-resolved policy from no overrides', () => {
      const engine = new PolicyEngine();
      const merged = engine.mergePolicy({});
      assert.equal(typeof merged.maxExecutionTimeMs, 'number');
      assert.equal(typeof merged.maxOutputBytes, 'number');
      assert.equal(typeof merged.allowNetwork, 'boolean');
      assert.equal(typeof merged.allowFilesystemWrite, 'boolean');
      assert.equal(typeof merged.allowDelete, 'boolean');
      assert.equal(typeof merged.allowBackgroundProcess, 'boolean');
      assert.equal(typeof merged.workingDirectory, 'string');
      assert.ok(merged.environment && typeof merged.environment === 'object');
      assert.ok(Array.isArray(merged.allowedCommands));
    });

    it('applies overrides on top of constructor defaults', () => {
      const engine = new PolicyEngine({
        maxExecutionTimeMs: 5000,
        allowNetwork: false,
      });
      const merged = engine.mergePolicy({});
      assert.equal(merged.maxExecutionTimeMs, 5000);
      assert.equal(merged.allowNetwork, false);
      // Fields not overridden should retain the built-in defaults.
      assert.ok(merged.maxExecutionBytes === undefined);
    });

    it('tool policy overrides constructor defaults', () => {
      const engine = new PolicyEngine({
        maxExecutionTimeMs: 1000,
        maxOutputBytes: 1024,
      });
      const merged = engine.mergePolicy({
        maxExecutionTimeMs: 2000,
      });
      assert.equal(merged.maxExecutionTimeMs, 2000);
      assert.equal(merged.maxOutputBytes, 1024);
    });

    it('shallow-merges environment variables', () => {
      const engine = new PolicyEngine({
        environment: { FOO: '1', BAR: '2' },
      });
      const merged = engine.mergePolicy({
        environment: { BAR: 'override', BAZ: '3' },
      });
      assert.equal(merged.environment.FOO, '1');
      assert.equal(merged.environment.BAR, 'override');
      assert.equal(merged.environment.BAZ, '3');
    });

    it('preserves empty allowedCommands (interpreted as "no restriction")', () => {
      const engine = new PolicyEngine();
      const merged = engine.mergePolicy({ allowedCommands: [] });
      assert.deepEqual(merged.allowedCommands, []);
    });
  });

  describe('validate() — maxExecutionTimeMs', () => {
    it('flags a non-positive maxExecutionTimeMs', () => {
      const engine = new PolicyEngine();
      const policy: ExecutionPolicy = {
        ...engine.mergePolicy({}),
        maxExecutionTimeMs: 0,
      };
      const violations = engine.validate(policy, { command: '/bin/echo', args: [] });
      assert.ok(
        violations.some((v) => v.rule === 'maxExecutionTimeMs'),
        'expected a maxExecutionTimeMs violation',
      );
    });

    it('flags a non-integer maxExecutionTimeMs', () => {
      const engine = new PolicyEngine();
      const policy: ExecutionPolicy = {
        ...engine.mergePolicy({}),
        maxExecutionTimeMs: 1.5,
      };
      const violations = engine.validate(policy, { command: '/bin/echo', args: [] });
      assert.ok(
        violations.some((v) => v.rule === 'maxExecutionTimeMs'),
      );
    });

    it('flags a maxExecutionTimeMs above the hard ceiling', () => {
      const engine = new PolicyEngine();
      const policy: ExecutionPolicy = {
        ...engine.mergePolicy({}),
        maxExecutionTimeMs: 600001,
      };
      const violations = engine.validate(policy, { command: '/bin/echo', args: [] });
      assert.ok(
        violations.some(
          (v) =>
            v.rule === 'maxExecutionTimeMs' &&
            v.message.includes('600000'),
        ),
      );
    });

    it('accepts a valid maxExecutionTimeMs', () => {
      const engine = new PolicyEngine();
      const policy = engine.mergePolicy({ maxExecutionTimeMs: 5000 });
      const violations = engine.validate(policy, { command: '/bin/echo', args: [] });
      const timingViolations = violations.filter((v) => v.rule === 'maxExecutionTimeMs');
      assert.equal(timingViolations.length, 0);
    });
  });

  describe('validate() — maxOutputBytes', () => {
    it('flags a non-positive maxOutputBytes', () => {
      const engine = new PolicyEngine();
      const policy: ExecutionPolicy = {
        ...engine.mergePolicy({}),
        maxOutputBytes: 0,
      };
      const violations = engine.validate(policy, { command: '/bin/echo', args: [] });
      assert.ok(violations.some((v) => v.rule === 'maxOutputBytes'));
    });

    it('flags a non-integer maxOutputBytes', () => {
      const engine = new PolicyEngine();
      const policy: ExecutionPolicy = {
        ...engine.mergePolicy({}),
        maxOutputBytes: 100.5,
      };
      const violations = engine.validate(policy, { command: '/bin/echo', args: [] });
      assert.ok(violations.some((v) => v.rule === 'maxOutputBytes'));
    });

    it('flags a maxOutputBytes above the hard ceiling', () => {
      const engine = new PolicyEngine();
      const policy: ExecutionPolicy = {
        ...engine.mergePolicy({}),
        maxOutputBytes: 100 * 1024 * 1024 + 1,
      };
      const violations = engine.validate(policy, { command: '/bin/echo', args: [] });
      assert.ok(violations.some((v) => v.rule === 'maxOutputBytes'));
    });

    it('accepts a valid maxOutputBytes', () => {
      const engine = new PolicyEngine();
      const policy = engine.mergePolicy({ maxOutputBytes: 1024 });
      const violations = engine.validate(policy, { command: '/bin/echo', args: [] });
      const outViolations = violations.filter((v) => v.rule === 'maxOutputBytes');
      assert.equal(outViolations.length, 0);
    });
  });

  describe('validate() — allowedCommands', () => {
    it('empty allowedCommands allows any command', () => {
      const engine = new PolicyEngine();
      const policy = engine.mergePolicy({ allowedCommands: [] });
      const violations = engine.validate(policy, {
        command: '/anything/at/all',
        args: [],
      });
      const cmdViolations = violations.filter((v) => v.rule === 'allowedCommands');
      assert.equal(cmdViolations.length, 0);
    });

    it('non-empty allowedCommands restricts to the listed entries', () => {
      const engine = new PolicyEngine();
      const policy = engine.mergePolicy({ allowedCommands: ['/bin/echo'] });
      const violations = engine.validate(policy, {
        command: '/bin/bash',
        args: ['-c', 'ls'],
      });
      assert.ok(
        violations.some(
          (v) => v.rule === 'allowedCommands' && v.message.includes('/bin/bash'),
        ),
      );
    });

    it('matches by command basename', () => {
      const engine = new PolicyEngine();
      const policy = engine.mergePolicy({ allowedCommands: ['echo'] });
      const violations = engine.validate(policy, {
        command: '/usr/bin/echo',
        args: ['hi'],
      });
      const cmdViolations = violations.filter((v) => v.rule === 'allowedCommands');
      assert.equal(cmdViolations.length, 0);
    });

    it('matches against an absolute path entry', () => {
      const engine = new PolicyEngine();
      const policy = engine.mergePolicy({ allowedCommands: ['/bin/echo'] });
      const violations = engine.validate(policy, {
        command: '/bin/echo',
        args: ['hi'],
      });
      const cmdViolations = violations.filter((v) => v.rule === 'allowedCommands');
      assert.equal(cmdViolations.length, 0);
    });

    it('skips empty entries in the allowed list', () => {
      const engine = new PolicyEngine();
      const policy = engine.mergePolicy({ allowedCommands: ['', '/bin/echo'] });
      const violations = engine.validate(policy, {
        command: '/bin/echo',
        args: [],
      });
      const cmdViolations = violations.filter((v) => v.rule === 'allowedCommands');
      assert.equal(cmdViolations.length, 0);
    });
  });

  describe('validate() — workingDirectory', () => {
    it('flags an empty workingDirectory', () => {
      const engine = new PolicyEngine();
      const policy: ExecutionPolicy = {
        ...engine.mergePolicy({}),
        workingDirectory: '',
      };
      const violations = engine.validate(policy, { command: '/bin/echo', args: [] });
      assert.ok(violations.some((v) => v.rule === 'workingDirectory'));
    });

    it('flags a non-string workingDirectory', () => {
      const engine = new PolicyEngine();
      const base = engine.mergePolicy({});
      const policy = {
        ...base,
        workingDirectory: undefined as unknown as string,
      };
      const violations = engine.validate(policy, { command: '/bin/echo', args: [] });
      assert.ok(violations.some((v) => v.rule === 'workingDirectory'));
    });

    it('accepts a valid workingDirectory', () => {
      const engine = new PolicyEngine();
      const policy = engine.mergePolicy({
        workingDirectory: '/tmp',
      });
      const violations = engine.validate(policy, { command: '/bin/echo', args: [] });
      const wdViolations = violations.filter((v) => v.rule === 'workingDirectory');
      assert.equal(wdViolations.length, 0);
    });
  });

  describe('validate() — combined violations', () => {
    it('reports all violations in a single call', () => {
      const engine = new PolicyEngine();
      const policy: ExecutionPolicy = {
        ...engine.mergePolicy({}),
        maxExecutionTimeMs: 0,
        maxOutputBytes: -1,
        workingDirectory: '',
      };
      const violations = engine.validate(policy, { command: '/bin/echo', args: [] });
      const rules = violations.map((v) => v.rule);
      assert.ok(rules.includes('maxExecutionTimeMs'));
      assert.ok(rules.includes('maxOutputBytes'));
      assert.ok(rules.includes('workingDirectory'));
    });

    it('returns an empty list for a fully valid policy', () => {
      const engine = new PolicyEngine();
      const policy = engine.mergePolicy({
        maxExecutionTimeMs: 10000,
        maxOutputBytes: 1024 * 1024,
        workingDirectory: '/tmp',
        allowedCommands: [],
      });
      const violations = engine.validate(policy, {
        command: '/bin/echo',
        args: ['hi'],
      });
      assert.deepEqual(violations, []);
    });
  });
});
