import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import { parseInstruction } from '../src/parser.js';

describe('parseInstruction', () => {
  it('parses a shell list command', () => {
    const plan = parseInstruction('list files in current directory');
    assert.equal(plan.steps[0].skill, 'shell');
    assert.equal(plan.steps[0].command, 'ls current directory');
  });

  it('parses a read file instruction', () => {
    const plan = parseInstruction('read file /etc/hosts');
    assert.equal(plan.steps[0].skill, 'file');
    assert.equal(plan.steps[0].action, 'read');
    assert.equal(plan.steps[0].payload, '/etc/hosts');
  });

  it('parses a create file instruction', () => {
    const plan = parseInstruction('create file /tmp/test.txt hello world');
    assert.equal(plan.steps[0].skill, 'file');
    assert.equal(plan.steps[0].action, 'create');
    assert.equal(plan.steps[0].payload, '/tmp/test.txt hello world');
  });
});
