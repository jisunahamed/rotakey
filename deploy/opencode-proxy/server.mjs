// Adapted from bigdata2211it-web/opencode-free-proxy (MIT), revision
// 49488fd2c6623cbcfdf11ef584722563df08aceb. Rotakey handles public protocol
// conversion; this private sidecar preserves the complete OpenAI wire body.
import http from 'node:http';
import https from 'node:https';
import crypto from 'node:crypto';
import { pathToFileURL } from 'node:url';
import { pipeline } from 'node:stream';

export const models = ['deepseek-v4-flash-free', 'big-pickle', 'minimax-m2.5-free', 'nemotron-3-super-free', 'qwen3.6-plus-free'];
const id = prefix => `${prefix}_${Date.now().toString(16)}${crypto.randomBytes(12).toString('hex')}`;

export function createProxy({ apiKey, origin = 'https://opencode.ai', timeout = 900_000 } = {}) {
  if (!apiKey || apiKey.length < 24) throw new Error('Set a private OPENCODE_PROXY_KEY of at least 24 characters');
  const server = http.createServer(async (req, res) => {
    const json = (code, data) => { res.writeHead(code, { 'Content-Type': 'application/json' }); res.end(JSON.stringify(data)); };
    if (req.method === 'GET' && req.url === '/health') return json(200, { status: 'ok', service: 'opencode-free-proxy', upstream_revision: '49488fd2' });
    const token = req.headers.authorization?.replace(/^Bearer /, '') || '';
    const a = Buffer.from(token), b = Buffer.from(apiKey);
    if (a.length !== b.length || !crypto.timingSafeEqual(a, b)) return json(401, { error: { message: 'Invalid proxy key' } });
    if (req.method === 'GET' && req.url === '/v1/models') return json(200, { object: 'list', data: models.map(model => ({ id: model, object: 'model', owned_by: 'opencode-free' })) });
    if (req.method !== 'POST' || !['/v1/chat/completions', '/v1/responses'].includes(req.url)) return json(404, { error: { message: 'Unknown proxy endpoint' } });
    const chunks = []; let size = 0;
    try {
      for await (const chunk of req) {
        size += chunk.length;
        if (size > 32 * 1024 * 1024) return json(413, { error: { message: 'Request exceeds 32 MB' } });
        chunks.push(chunk);
      }
    } catch { return; }
    if (res.destroyed) return;
    const body = Buffer.concat(chunks);
    try { JSON.parse(body); } catch { return json(400, { error: { message: 'Invalid JSON' } }); }
    const target = new URL('/zen' + req.url, origin);
    const send = target.protocol === 'https:' ? https.request : http.request;
    let upstreamResponse;
    const upstream = send(target, {
      method: 'POST', headers: {
        'Content-Type': 'application/json', 'Content-Length': body.length,
        Authorization: 'Bearer public',
        'User-Agent': 'opencode/1.15.0 ai-sdk/provider-utils/4.0.23 runtime/bun/1.3.13',
        'x-opencode-client': 'cli', 'x-opencode-project': 'global',
        'x-opencode-request': id('msg'), 'x-opencode-session': id('ses'),
      },
    }, incoming => {
      upstreamResponse = incoming;
      if (res.destroyed) { incoming.destroy(); return; }
      const headers = {};
      for (const name of ['content-type', 'cache-control', 'retry-after', 'request-id', 'x-request-id', 'content-encoding']) {
        if (incoming.headers[name]) headers[name] = incoming.headers[name];
      }
      headers['x-accel-buffering'] = 'no';
      // Forward real status and bytes, including successful empty bodies and
      // error SSE. Never infer a 429/502 from text or manufacture output.
      res.writeHead(incoming.statusCode, headers); res.flushHeaders();
      pipeline(incoming, res, () => { clearTimeout(timer); });
    });
    const timer = setTimeout(() => upstream.destroy(new Error('upstream timeout')), timeout);
    timer.unref();
    const cancel = () => {
      if (!res.writableFinished) { upstream.destroy(); upstreamResponse?.destroy(); }
      clearTimeout(timer);
    };
    res.once('close', cancel);
    upstream.once('error', () => {
      clearTimeout(timer);
      if (res.destroyed) return;
      if (!res.headersSent) json(502, { error: { message: 'OpenCode upstream connection failed' } });
      else res.destroy();
    });
    upstream.end(body);
  });
  server.requestTimeout = 0;
  server.headersTimeout = 30_000;
  return server;
}

if (import.meta.url === pathToFileURL(process.argv[1] || '').href) {
  const server = createProxy({ apiKey: process.env.OPENCODE_PROXY_KEY });
  server.listen(Number(process.env.PROXY_PORT || 6446), '0.0.0.0', () => console.log('OpenCode proxy ready'));
  for (const signal of ['SIGTERM', 'SIGINT']) process.on(signal, () => server.close(() => process.exit(0)));
}
