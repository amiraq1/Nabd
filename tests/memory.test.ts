import { describe, it } from 'node:test';
import assert from 'node:assert';
import { SemanticMemory } from '../src/core/memory/SemanticMemory.js';
import fs from 'node:fs';
import path from 'node:path';

describe('Phase 7 - Semantic Memory', () => {
  const dbPath = path.join(process.cwd(), '.test_memory.json');

  it('remembers and recalls semantic context', () => {
    if (fs.existsSync(dbPath)) fs.unlinkSync(dbPath);
    
    const memory = new SemanticMemory(dbPath);
    const id1 = memory.remember('The user prefers neon cyberpunk aesthetics.', ['style', 'ui']);
    const id2 = memory.remember('Project NABD_OS is written in TypeScript.', ['tech', 'stack']);
    
    const results = memory.recall('cyberpunk ui');
    assert.equal(results.length > 0, true);
    assert.equal(results[0].id, id1);

    const results2 = memory.recall('typescript');
    assert.equal(results2.length > 0, true);
    assert.equal(results2[0].id, id2);

    memory.clear();
    assert.equal(memory.recall('cyberpunk').length, 0);

    if (fs.existsSync(dbPath)) fs.unlinkSync(dbPath);
  });
});
