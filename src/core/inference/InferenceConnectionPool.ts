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

  /**
   * Performs an HTTP request using the shared pool.
   * Exposes the raw response for precise stream control (backpressure).
   */
  request(url: URL, options: http.RequestOptions, body?: string | Buffer): Promise<http.IncomingMessage> {
    return new Promise((resolve, reject) => {
      const isHttps = url.protocol === 'https:';
      const agent = isHttps ? this.httpsAgent : this.httpAgent;
      const requestFn = isHttps ? https.request : http.request;

      const reqOpts: http.RequestOptions = {
        ...options,
        agent,
        timeout: this.timeoutMs,
      };

      const req = requestFn(url, reqOpts, (res) => {
        resolve(res);
      });

      req.on('error', (err) => {
        reject(err);
      });

      req.on('timeout', () => {
        req.destroy(new Error('ConnectionPool: Request timed out'));
      });

      if (body) {
        req.write(body);
      }
      req.end();
    });
  }

  async healthCheck(urlStr: string): Promise<boolean> {
    try {
      const url = new URL(urlStr);
      const res = await this.request(url, { method: 'GET' });
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
