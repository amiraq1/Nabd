import http from 'node:http';
import https from 'node:https';

export interface PoolConfig {
  maxSockets: number;
  maxFreeSockets: number;
  timeoutMs: number;
  keepAlive: boolean;
  keepAliveMsecs: number;
}

export class InferenceConnectionPool {
  private httpAgent: http.Agent;
  private httpsAgent: https.Agent;
  private timeoutMs: number;

  constructor(config: Partial<PoolConfig> = {}) {
    const opts = {
      maxSockets: config.maxSockets ?? 10,
      maxFreeSockets: config.maxFreeSockets ?? 5,
      timeout: config.timeoutMs ?? 30000,
      keepAlive: config.keepAlive ?? true,
      keepAliveMsecs: config.keepAliveMsecs ?? 15000,
    };

    this.timeoutMs = opts.timeout;
    this.httpAgent = new http.Agent(opts);
    this.httpsAgent = new https.Agent(opts);
  }

  async request(
    url: URL,
    options: http.RequestOptions,
    body?: string | Buffer,
    retries = 3,
  ): Promise<http.IncomingMessage> {
    for (let attempt = 0; attempt <= retries; attempt++) {
      const res = await this._rawRequest(url, options, body);

      if (res.statusCode === 429 || (res.statusCode && res.statusCode >= 500)) {
        res.resume(); // استهلك الـ body لتحرير الـ socket
        if (attempt === retries) {
          throw new Error(`HTTP ${res.statusCode} after ${retries} retries`);
        }
        const retryAfter = res.headers['retry-after'];
        const delayMs = retryAfter
          ? parseInt(String(retryAfter), 10) * 1000
          : Math.min(1000 * 2 ** attempt, 10000);
        await new Promise((r) => setTimeout(r, delayMs));
        continue;
      }

      if (res.statusCode && res.statusCode >= 400) {
        throw new Error(`HTTP ${res.statusCode}: request failed`);
      }

      return res;
    }
    throw new Error('Unreachable');
  }

  private _rawRequest(
    url: URL,
    options: http.RequestOptions,
    body?: string | Buffer,
  ): Promise<http.IncomingMessage> {
    return new Promise((resolve, reject) => {
      const isHttps = url.protocol === 'https:';
      const agent = isHttps ? this.httpsAgent : this.httpAgent;
      const requestFn = isHttps ? https.request : http.request;

      const reqOpts: http.RequestOptions = {
        ...options,
        agent,
        timeout: this.timeoutMs,
      };

      const req = requestFn(url, reqOpts, (res) => resolve(res));
      req.on('error', reject);
      req.on('timeout', () => req.destroy(new Error('ConnectionPool: Request timed out')));
      if (body) req.write(body);
      req.end();
    });
  }

  async healthCheck(urlStr: string): Promise<boolean> {
    try {
      const url = new URL(urlStr);
      const res = await this.request(url, { method: 'GET' }, undefined, 0);
      res.resume(); // consume body immediately
      return res.statusCode !== undefined && res.statusCode >= 200 && res.statusCode < 400;
    } catch {
      return false;
    }
  }

  destroy(): void {
    this.httpAgent.destroy();
    this.httpsAgent.destroy();
  }
}

export const connectionPool = new InferenceConnectionPool();
