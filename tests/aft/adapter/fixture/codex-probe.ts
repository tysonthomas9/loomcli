import { FixtureError } from './lifecycle.js';

export type CodexProtocolProbe = (endpoint: string, signal: AbortSignal) => Promise<void>;
/** Fixed initialize/initialized handshake, matching codex_client.go. This sends
 * no thread, turn, model or provider request. Endpoint comes from an owned port. */
export function createCodexProtocolProbe(connect: (endpoint: string) => WebSocket = endpoint => new WebSocket(endpoint)): CodexProtocolProbe {
  return (endpoint, signal) => new Promise<void>((resolve, reject) => {
    signal.throwIfAborted();
    if (!/^ws:\/\/127\.0\.0\.1:\d+$/.test(endpoint)) throw new FixtureError('ownership-mismatch');
    const socket = connect(endpoint); let finished = false; let opened = false;
    const finish = (ok: boolean) => {
      if (finished) return; finished = true;
      signal.removeEventListener('abort', abort); socket.close();
      if (ok) resolve(); else reject(new FixtureError('observation-failed'));
    };
    const abort = () => finish(false); signal.addEventListener('abort', abort, { once: true });
    socket.addEventListener('error', () => finish(false)); socket.addEventListener('close', () => finish(false));
    socket.addEventListener('open', () => {
      opened = true;
      socket.send(JSON.stringify({ id: 1, method: 'initialize', params: {
        clientInfo: { name: 'loom', title: 'Loom', version: 'dev' }, capabilities: { experimentalApi: true },
      } }));
    });
    socket.addEventListener('message', event => {
      if (!opened || typeof event.data !== 'string' || Buffer.byteLength(event.data) > 65536) return finish(false);
      try {
        const message = JSON.parse(event.data);
        if (message.id !== 1) return finish(false);
        if (message.error || !message.result || typeof message.result !== 'object') return finish(false);
        socket.send(JSON.stringify({ method: 'initialized', params: null })); finish(true);
      } catch { finish(false); }
    });
    if (signal.aborted) abort();
  });
}
export const initializeCodex = createCodexProtocolProbe();
