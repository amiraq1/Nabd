import { readFileSync, existsSync, mkdirSync } from 'node:fs';
import { homedir } from 'node:os';
import { join } from 'node:path';

export interface NabdConfig {
  provider: string;
  model: string;
  endpoint: string;
  nvidiaApiKey?: string;
  openaiApiKey?: string;
  maxContextTokens?: number;
  vectorDbPath?: string;
}

function validateConfig(config: NabdConfig): void {
  if (!config.provider) throw new Error('تكوين خاطئ: يجب تحديد مزود الخدمة (provider).');
  if (config.provider !== 'ollama' && !config.nvidiaApiKey && !config.openaiApiKey) {
    throw new Error('تكوين خاطئ: المزود السحابي يحتاج لمفتاح API.');
  }
}

export function loadNabdConfig(): NabdConfig {
  const configDir = join(homedir(), '.config', 'nabd');
  const configPath = join(configDir, 'config.json');
  
  if (!existsSync(configDir)) {
    mkdirSync(configDir, { recursive: true });
  }

  let parsed: any = null;

  if (existsSync(configPath)) {
    try {
      const raw = readFileSync(configPath, 'utf-8');
      parsed = JSON.parse(raw);
    } catch (e) {
      console.error('Failed to parse config:', e);
    }
  }
  
  // إذا كان التنسيق الجديد موجوداً (يحتوي على activeProfile)
  if (parsed && parsed.activeProfile) {
    const isNvidia = parsed.activeProfile.provider === 'nvidia';
    const isOpenAI = parsed.activeProfile.provider === 'openai';
    
    // إعداد الـ Endpoint المناسب للخدمة السحابية
    let endpoint = process.env.OLLAMA_URL || 'http://localhost:11434';
    if (isNvidia) {
      endpoint = 'https://integrate.api.nvidia.com/v1';
    } else if (isOpenAI) {
      // قد نستخدم نقطة نهاية مخصصة كـ OpenRouter إذا لم يكن OpenAI صريحاً
      endpoint = 'https://api.openai.com/v1'; 
    }
    
    const config: NabdConfig = {
      provider: parsed.activeProfile.provider || 'ollama',
      model: parsed.activeProfile.model || 'llama3',
      endpoint: endpoint,
      nvidiaApiKey: parsed.secrets?.nvidiaApiKey,
      openaiApiKey: parsed.secrets?.openrouterApiKey || parsed.secrets?.openaiApiKey,
      maxContextTokens: parsed.activeProfile.maxContextTokens,
      vectorDbPath: parsed.storage?.vectorDbPath
    };
    validateConfig(config);
    return config;
  }

  // دعم التنسيق القديم أو الافتراضي
  const config: NabdConfig = {
    provider: parsed?.provider || 'ollama',
    model: parsed?.model || process.env.OLLAMA_MODEL || 'llama3',
    endpoint: parsed?.endpoint || process.env.OLLAMA_URL || 'http://localhost:11434',
    nvidiaApiKey: parsed?.nvidiaApiKey,
    openaiApiKey: parsed?.openaiApiKey
  };
  validateConfig(config);
  return config;
}

export const globalConfig = loadNabdConfig();
