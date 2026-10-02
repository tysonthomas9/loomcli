import { readFileSync, writeFileSync, existsSync } from 'node:fs';

const INDEX = new URL('./public/index.html', import.meta.url);

export function createStore(file) {
  const data = file && existsSync(file)
    ? JSON.parse(readFileSync(file, 'utf8'))
    : { channels: ['general', 'random'], messages: [] };
  const save = () => file && writeFileSync(file, JSON.stringify(data, null, 2));
  return {
    channels: () => data.channels,
    messages: (channel) => data.messages.filter((m) => m.channel === channel),
    post(channel, user, text) {
      if (!data.channels.includes(channel)) throw new Error('unknown channel');
      if (!text || !text.trim()) throw new Error('empty message');
      const msg = { id: (data.messages.at(-1)?.id ?? 0) + 1, channel, user: user || 'anon', text: text.trim(), at: new Date().toISOString() };
      data.messages.push(msg);
      save();
      return msg;
    },
  };
}

function send(res, status, body, type = 'application/json') {
  res.writeHead(status, { 'content-type': type });
  res.end(type === 'application/json' ? JSON.stringify(body) : body);
}

export function createHandler(store) {
  return (req, res) => {
    const url = new URL(req.url, 'http://x');
    const match = url.pathname.match(/^\/api\/channels\/([^/]+)\/messages$/);
    if (req.method === 'GET' && url.pathname === '/') return send(res, 200, readFileSync(INDEX), 'text/html');
    if (req.method === 'GET' && url.pathname === '/api/channels') return send(res, 200, store.channels());
    if (match && req.method === 'GET') return send(res, 200, store.messages(match[1]));
    if (match && req.method === 'POST') {
      let body = '';
      req.on('data', (c) => (body += c));
      req.on('end', () => {
        try {
          const { user, text } = JSON.parse(body || '{}');
          send(res, 201, store.post(match[1], user, text));
        } catch (err) {
          send(res, 400, { error: err.message });
        }
      });
      return;
    }
    send(res, 404, { error: 'not found' });
  };
}
