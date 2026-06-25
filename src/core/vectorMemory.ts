import fs from 'node:fs';
import path from 'node:path';
import { loadConfig } from './configManager.js';

const config = loadConfig();
const MEMORY_FILE = path.join(path.dirname(config.vectorDbPath), 'semantic_store.json');

export interface MemoryNode {
  id: string;
  text: string;
  vector: number[];
  timestamp: number;
}

// 1. التهيئة المكانية للذاكرة
const loadMemoryStore = (): MemoryNode[] => {
  if (!fs.existsSync(MEMORY_FILE)) return [];
  return JSON.parse(fs.readFileSync(MEMORY_FILE, 'utf-8'));
};

const saveMemoryStore = (store: MemoryNode[]) => {
  fs.writeFileSync(MEMORY_FILE, JSON.stringify(store), 'utf-8');
};

// 2. الاتصال بمحرك التضمين المحلي (Local Embedding API)
export const generateEmbedding = async (text: string): Promise<number[]> => {
  // Using the base endpoint configured in configManager.ts
  const response = await fetch(`${config.endpoint}/api/embeddings`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ model: config.model, prompt: text }),
  });
  
  if (!response.ok) throw new Error('Embedding generation failed.');
  const data = await response.json();
  return data.embedding; // يفترض أن خادم Ollama يرجع مصفوفة الأرقام هنا
};

// 3. الرياضيات البحتة: حساب تشابه جيب التمام (Cosine Similarity)
const cosineSimilarity = (vecA: number[], vecB: number[]): number => {
  let dotProduct = 0;
  let normA = 0;
  let normB = 0;
  for (let i = 0; i < vecA.length; i++) {
    dotProduct += vecA[i] * vecB[i];
    normA += vecA[i] * vecA[i];
    normB += vecB[i] * vecB[i];
  }
  if (normA === 0 || normB === 0) return 0;
  return dotProduct / (Math.sqrt(normA) * Math.sqrt(normB));
};

// 4. واجهة استرجاع المتجهات (Vector Retrieval Pipeline)
export const searchSemanticMemory = async (query: string, topK: number = 3): Promise<MemoryNode[]> => {
  const queryVector = await generateEmbedding(query);
  const store = loadMemoryStore();
  
  return store
    .map(node => ({
      ...node,
      score: cosineSimilarity(queryVector, node.vector)
    }))
    .sort((a, b) => (b as any).score - (a as any).score)
    .slice(0, topK);
};

export const insertSemanticMemory = async (text: string) => {
  const vector = await generateEmbedding(text);
  const store = loadMemoryStore();
  
  store.push({
    id: Date.now().toString(),
    text,
    vector,
    timestamp: Date.now()
  });
  
  saveMemoryStore(store);
};
