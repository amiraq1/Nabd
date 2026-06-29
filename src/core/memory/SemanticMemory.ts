import fs from 'node:fs';
import path from 'node:path';

export interface MemoryEntry {
  id: string;
  timestamp: number;
  tags: string[];
  content: string;
}

export class SemanticMemory {
  private dbPath: string;
  private entries: MemoryEntry[] = [];

  constructor(dbPath?: string) {
    this.dbPath = dbPath || path.join(process.cwd(), '.nabd_memory.json');
    this.load();
  }

  private load(): void {
    try {
      if (fs.existsSync(this.dbPath)) {
        const data = fs.readFileSync(this.dbPath, 'utf8');
        this.entries = JSON.parse(data);
      }
    } catch (error) {
      console.warn(`Failed to load memory from ${this.dbPath}:`, error);
      this.entries = [];
    }
  }

  private save(): void {
    try {
      fs.writeFileSync(this.dbPath, JSON.stringify(this.entries, null, 2), 'utf8');
    } catch (error) {
      console.error(`Failed to save memory to ${this.dbPath}:`, error);
    }
  }

  public forceSave(): void {
    this.save();
  }

  public remember(content: string, tags: string[] = []): string {
    const id = Math.random().toString(36).substring(2, 15);
    const entry: MemoryEntry = {
      id,
      timestamp: Date.now(),
      tags,
      content,
    };
    this.entries.push(entry);
    this.save();
    return id;
  }

  public recall(query: string, limit: number = 5): MemoryEntry[] {
    // A simplified keyword-based recall mechanism replacing true vector search.
    // In a real system, this would use embeddings (e.g., cosine similarity).
    const terms = query.toLowerCase().split(/\s+/);
    
    const scored = this.entries.map(entry => {
      let score = 0;
      const contentLower = entry.content.toLowerCase();
      const tagsLower = entry.tags.map(t => t.toLowerCase());

      for (const term of terms) {
        if (contentLower.includes(term)) score += 1;
        if (tagsLower.includes(term)) score += 2;
      }
      return { entry, score };
    });

    return scored
      .filter(s => s.score > 0)
      .sort((a, b) => b.score - a.score || b.entry.timestamp - a.entry.timestamp)
      .slice(0, limit)
      .map(s => s.entry);
  }

  public clear(): void {
    this.entries = [];
    this.save();
  }
}

export const semanticMemory = new SemanticMemory();
