import { SchemaValidationError } from '../errors.js';
import type { ToolDefinition } from '../types.js';

export class SchemaValidator {
  /**
   * Validates the provided arguments against the tool's parameter schema.
   * Throws SchemaValidationError on failure.
   */
  validate(tool: ToolDefinition, args: unknown): void {
    const schema = tool.parameters;
    this.validateNode(args, schema, '');
  }

  private validateNode(data: unknown, schema: any, path: string): void {
    if (!schema || typeof schema !== 'object') return;

    if (schema.type) {
      this.validateType(data, schema.type, path);
    }

    if (schema.enum && Array.isArray(schema.enum)) {
      if (!schema.enum.includes(data)) {
        throw new SchemaValidationError(`Validation failed at '${path || '$'}': Expected one of [${schema.enum.join(', ')}], but got ${JSON.stringify(data)}`);
      }
    }

    if (schema.type === 'object' && typeof data === 'object' && data !== null && !Array.isArray(data)) {
      const dataObj = data as Record<string, unknown>;
      
      if (schema.required && Array.isArray(schema.required)) {
        for (const req of schema.required) {
          if (!(req in dataObj)) {
            throw new SchemaValidationError(`Validation failed at '${path || '$'}': Missing required property '${req}'`);
          }
        }
      }

      if (schema.properties) {
        for (const key of Object.keys(schema.properties)) {
          if (key in dataObj) {
            const childPath = path ? `${path}.${key}` : key;
            this.validateNode(dataObj[key], schema.properties[key], childPath);
          }
        }
      }

      if (schema.additionalProperties === false) {
        const allowedKeys = schema.properties ? Object.keys(schema.properties) : [];
        for (const key of Object.keys(dataObj)) {
          if (!allowedKeys.includes(key)) {
            throw new SchemaValidationError(`Validation failed at '${path || '$'}': Additional properties are not allowed ('${key}' was unexpected)`);
          }
        }
      }
    }

    if (schema.type === 'array' && Array.isArray(data)) {
      if (schema.items) {
        for (let i = 0; i < data.length; i++) {
          const childPath = path ? `${path}[${i}]` : `[${i}]`;
          this.validateNode(data[i], schema.items, childPath);
        }
      }
    }

    if (schema.type === 'string' && typeof data === 'string' && schema.pattern) {
      const regex = new RegExp(schema.pattern);
      if (!regex.test(data)) {
        throw new SchemaValidationError(`Validation failed at '${path || '$'}': String does not match pattern ${schema.pattern}`);
      }
    }
  }

  private validateType(data: unknown, type: string | string[], path: string): void {
    const types = Array.isArray(type) ? type : [type];
    
    let isValid = false;
    for (const t of types) {
      if (t === 'string' && typeof data === 'string') isValid = true;
      else if (t === 'number' && typeof data === 'number') isValid = true;
      else if (t === 'integer' && typeof data === 'number' && Number.isInteger(data)) isValid = true;
      else if (t === 'boolean' && typeof data === 'boolean') isValid = true;
      else if (t === 'object' && typeof data === 'object' && data !== null && !Array.isArray(data)) isValid = true;
      else if (t === 'array' && Array.isArray(data)) isValid = true;
      else if (t === 'null' && data === null) isValid = true;
    }

    if (!isValid) {
      throw new SchemaValidationError(`Validation failed at '${path || '$'}': Expected type ${types.join(' or ')}, but got ${Array.isArray(data) ? 'array' : data === null ? 'null' : typeof data}`);
    }
  }
}

export const schemaValidator = new SchemaValidator();
