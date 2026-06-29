import { describe, it, beforeEach } from 'node:test';
import assert from 'node:assert';
import { ToolRegistry } from '../src/core/tools/ToolRegistry.ts';
import { CapabilityResolver } from '../src/core/tools/CapabilityResolver.ts';
import { SchemaValidator } from '../src/core/tools/SchemaValidator.ts';
import { PermissionResolver } from '../src/core/tools/PermissionResolver.ts';
import { ManifestGenerator } from '../src/core/tools/ManifestGenerator.ts';
import { validateRuntime } from '../src/core/tools/RuntimeSelfValidator.ts';
import type { ToolDefinition } from '../src/core/types.ts';
import { ToolNotFoundError, SchemaValidationError, PermissionDeniedError } from '../src/core/errors.ts';

const createDummyTool = (overrides: Partial<ToolDefinition> = {}): ToolDefinition => ({
  id: 'tool-dummy-1',
  name: 'dummy',
  description: 'A dummy tool',
  version: '1.0.0',
  category: 'test',
  parameters: { type: 'object', properties: { arg1: { type: 'string' } }, required: ['arg1'] },
  permissions: ['safe'],
  visibility: 'stable',
  execute: async () => ({} as any),
  ...overrides,
});

describe('Phase 4 - Capability Runtime', () => {
  let registry: ToolRegistry;
  let resolver: CapabilityResolver;
  let schemaValidator: SchemaValidator;
  let permissionResolver: PermissionResolver;
  let manifestGen: ManifestGenerator;

  beforeEach(() => {
    registry = new ToolRegistry();
    resolver = new CapabilityResolver(registry);
    schemaValidator = new SchemaValidator();
    permissionResolver = new PermissionResolver();
    manifestGen = new ManifestGenerator(registry);
  });

  describe('ToolRegistry (Dynamic Registration & Duplicate Detection)', () => {
    it('registers a tool successfully', () => {
      const tool = createDummyTool();
      registry.register(tool);
      assert.ok(registry.exists('dummy'));
      assert.equal(registry.list().length, 1);
    });

    it('prevents duplicate tool IDs', () => {
      registry.register(createDummyTool({ name: 'dummy1' }));
      assert.throws(() => {
        registry.register(createDummyTool({ name: 'dummy2' })); // same ID
      }, /duplicate tool id/);
    });

    it('prevents duplicate tool names and aliases', () => {
      registry.register(createDummyTool({ id: 't1', name: 'cmd', aliases: ['alias1'] }));
      assert.throws(() => {
        registry.register(createDummyTool({ id: 't2', name: 'other', aliases: ['cmd'] }));
      }, /duplicate/);
    });

    it('unregisters a tool completely', () => {
      registry.register(createDummyTool({ aliases: ['alias1'] }));
      assert.ok(registry.exists('alias1'));
      registry.unregister('tool-dummy-1');
      assert.equal(registry.exists('dummy'), false);
      assert.equal(registry.exists('alias1'), false);
    });

    it('freezes registry to prevent further registrations', () => {
      registry.freeze();
      assert.throws(() => {
        registry.register(createDummyTool());
      }, /frozen/);
    });
  });

  describe('CapabilityResolver (Alias Resolution & Unknown Rejection)', () => {
    it('resolves canonical tool from name', () => {
      const tool = createDummyTool();
      registry.register(tool);
      const resolved = resolver.resolve('dummy');
      assert.equal(resolved.id, 'tool-dummy-1');
    });

    it('resolves canonical tool from alias (deprecated alias support)', () => {
      const tool = createDummyTool({ aliases: ['old_dummy'] });
      registry.register(tool);
      const resolved = resolver.resolve('old_dummy');
      assert.equal(resolved.id, 'tool-dummy-1');
      assert.equal(resolved.name, 'dummy');
    });

    it('throws ToolNotFoundError for unknown tools', () => {
      assert.throws(() => {
        resolver.resolve('unknown');
      }, ToolNotFoundError);
    });
  });

  describe('SchemaValidator', () => {
    const tool = createDummyTool({
      parameters: {
        type: 'object',
        properties: {
          str: { type: 'string' },
          num: { type: 'number' },
          choice: { type: 'string', enum: ['A', 'B'] },
        },
        required: ['str'],
        additionalProperties: false
      }
    });

    it('passes valid arguments', () => {
      schemaValidator.validate(tool, { str: 'hello', num: 123, choice: 'A' });
    });

    it('throws on missing required property', () => {
      assert.throws(() => {
        schemaValidator.validate(tool, { num: 123 });
      }, SchemaValidationError);
    });

    it('throws on wrong type', () => {
      assert.throws(() => {
        schemaValidator.validate(tool, { str: 123 });
      }, SchemaValidationError);
    });

    it('throws on enum violation', () => {
      assert.throws(() => {
        schemaValidator.validate(tool, { str: 'hi', choice: 'C' });
      }, SchemaValidationError);
    });

    it('throws on additional properties', () => {
      assert.throws(() => {
        schemaValidator.validate(tool, { str: 'hi', extra: true });
      }, SchemaValidationError);
    });
  });

  describe('PermissionResolver', () => {
    const tool = createDummyTool({ permissions: ['filesystem', 'network'] });

    it('passes when all required permissions are allowed', () => {
      permissionResolver.verify(tool, { 
        role: 'ROOT_AGENT', 
        permissions: ['filesystem', 'network', 'safe'],
        sessionId: 'test',
        workspaceRoot: '/',
        networkPolicy: 'allow',
        filesystemPolicy: 'read_write',
        createdAt: Date.now()
      });
    });

    it('throws PermissionDeniedError when a permission is missing', () => {
      assert.throws(() => {
        permissionResolver.verify(tool, {
          role: 'ROOT_AGENT',
          permissions: ['filesystem'], // missing network
          sessionId: 'test',
          workspaceRoot: '/',
          networkPolicy: 'allow',
          filesystemPolicy: 'read_write',
          createdAt: Date.now()
        });
      }, PermissionDeniedError);
    });
  });

  describe('ManifestGenerator', () => {
    beforeEach(() => {
      registry.register(createDummyTool({ id: 't1', name: 't_stable', visibility: 'stable' }));
      registry.register(createDummyTool({ id: 't2', name: 't_exp', visibility: 'experimental' }));
      registry.register(createDummyTool({ id: 't3', name: 't_hidden', visibility: 'hidden' }));
    });

    it('filters hidden tools by default', () => {
      const json = JSON.parse(manifestGen.generateJSON());
      assert.equal(json.length, 1);
      assert.equal(json[0].name, 't_stable');
    });

    it('includes hidden tools if requested', () => {
      const json = JSON.parse(manifestGen.generateJSON(true));
      assert.equal(json.length, 3);
    });

    it('generates a stable fingerprint', () => {
      const hash1 = manifestGen.generateFingerprint();
      const hash2 = manifestGen.generateFingerprint();
      assert.equal(hash1, hash2);
      
      registry.register(createDummyTool({ id: 't4', name: 'new', visibility: 'stable' }));
      const hash3 = manifestGen.generateFingerprint();
      assert.notEqual(hash1, hash3);
    });
  });

  describe('RuntimeSelfValidator', () => {
    it('passes valid registry', () => {
      registry.register(createDummyTool());
      validateRuntime(registry);
    });

    it('fails if getPolicy crashes', () => {
      registry.register(createDummyTool({
        getPolicy: () => { throw new Error('crash') }
      }));
      assert.throws(() => validateRuntime(registry), /crashed during validation/);
    });
    
    it('fails on invalid permission', () => {
      registry.register(createDummyTool({
        permissions: ['invalid_perm' as any]
      }));
      assert.throws(() => validateRuntime(registry), /invalid permission/);
    });
  });
});
