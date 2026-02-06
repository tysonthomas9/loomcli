import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';

import { get, post, patch, del, ApiError } from './client';

describe('API Client', () => {
  let originalFetch: typeof global.fetch;

  beforeEach(() => {
    originalFetch = global.fetch;
    vi.useFakeTimers();
  });

  afterEach(() => {
    global.fetch = originalFetch;
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  describe('GET requests', () => {
    it('returns parsed JSON on successful request', async () => {
      const mockData = { id: 1, name: 'Test' };
      global.fetch = vi.fn().mockResolvedValue({
        ok: true,
        status: 200,
        json: () => Promise.resolve(mockData),
      });

      const result = await get<typeof mockData>('/api/test');

      expect(result).toEqual(mockData);
      expect(global.fetch).toHaveBeenCalledWith(
        '/api/test',
        expect.objectContaining({
          method: 'GET',
          headers: expect.objectContaining({
            Accept: 'application/json',
          }),
          body: null,
        })
      );
    });

    it('does not include Content-Type header for requests without body', async () => {
      global.fetch = vi.fn().mockResolvedValue({
        ok: true,
        status: 200,
        json: () => Promise.resolve({}),
      });

      await get('/api/test');

      const mockFn = global.fetch as ReturnType<typeof vi.fn>;
      const call = mockFn.mock.calls[0];
      expect(call).toBeDefined();
      const options = call?.[1] as { headers: Record<string, string> };
      expect(options.headers).not.toHaveProperty('Content-Type');
    });
  });

  describe('POST requests', () => {
    it('sends body and returns response', async () => {
      const requestBody = { name: 'New Item' };
      const responseData = { id: 1, name: 'New Item' };
      global.fetch = vi.fn().mockResolvedValue({
        ok: true,
        status: 201,
        json: () => Promise.resolve(responseData),
      });

      const result = await post<typeof responseData>('/api/items', requestBody);

      expect(result).toEqual(responseData);
      expect(global.fetch).toHaveBeenCalledWith(
        '/api/items',
        expect.objectContaining({
          method: 'POST',
          body: JSON.stringify(requestBody),
        })
      );
    });

    it('sets Content-Type header for requests with body', async () => {
      global.fetch = vi.fn().mockResolvedValue({
        ok: true,
        status: 201,
        json: () => Promise.resolve({}),
      });

      await post('/api/items', { data: 'test' });

      const mockFn = global.fetch as ReturnType<typeof vi.fn>;
      const call = mockFn.mock.calls[0];
      expect(call).toBeDefined();
      const options = call?.[1] as { headers: Record<string, string> };
      expect(options.headers).toHaveProperty('Content-Type', 'application/json');
    });
  });

  describe('PATCH requests', () => {
    it('sends partial body and returns response', async () => {
      const partialUpdate = { name: 'Updated Name' };
      const responseData = { id: 1, name: 'Updated Name', age: 30 };
      global.fetch = vi.fn().mockResolvedValue({
        ok: true,
        status: 200,
        json: () => Promise.resolve(responseData),
      });

      const result = await patch<typeof responseData>('/api/items/1', partialUpdate);

      expect(result).toEqual(responseData);
      expect(global.fetch).toHaveBeenCalledWith(
        '/api/items/1',
        expect.objectContaining({
          method: 'PATCH',
          body: JSON.stringify(partialUpdate),
        })
      );
    });
  });

  describe('DELETE requests', () => {
    it('works correctly', async () => {
      global.fetch = vi.fn().mockResolvedValue({
        ok: true,
        status: 204,
        json: () => Promise.reject(new Error('No content')),
      });

      const result = await del('/api/items/1');

      expect(result).toBeUndefined();
      expect(global.fetch).toHaveBeenCalledWith(
        '/api/items/1',
        expect.objectContaining({
          method: 'DELETE',
          body: null,
        })
      );
    });
  });

  describe('Error handling', () => {
    it('throws ApiError with status 404 for not found', async () => {
      const errorBody = { error: 'Not found' };
      global.fetch = vi.fn().mockResolvedValue({
        ok: false,
        status: 404,
        statusText: 'Not Found',
        text: () => Promise.resolve(JSON.stringify(errorBody)),
      });

      await expect(get('/api/items/999')).rejects.toThrow(ApiError);
      await expect(get('/api/items/999')).rejects.toMatchObject({
        status: 404,
        statusText: 'Not Found',
        body: errorBody,
      });
    });

    it('throws ApiError with status 500 for server error', async () => {
      const errorBody = { error: 'Internal server error' };
      global.fetch = vi.fn().mockResolvedValue({
        ok: false,
        status: 500,
        statusText: 'Internal Server Error',
        text: () => Promise.resolve(JSON.stringify(errorBody)),
      });

      await expect(get('/api/broken')).rejects.toThrow(ApiError);
      await expect(get('/api/broken')).rejects.toMatchObject({
        status: 500,
        statusText: 'Internal Server Error',
        body: errorBody,
      });
    });

    it('throws ApiError with status 0 for network error', async () => {
      global.fetch = vi.fn().mockRejectedValue(new TypeError('Failed to fetch'));

      await expect(get('/api/test')).rejects.toThrow(ApiError);
      await expect(get('/api/test')).rejects.toMatchObject({
        status: 0,
        statusText: 'Network error',
      });
    });

    it('throws ApiError with status 0 for timeout', async () => {
      // Use real timers for this test since fake timers interact poorly with AbortController
      vi.useRealTimers();

      // Create an AbortError like the browser would
      const abortError = new DOMException('The operation was aborted.', 'AbortError');

      global.fetch = vi.fn().mockImplementation((_url, options) => {
        return new Promise((_, reject) => {
          // Listen for abort signal
          if (options?.signal) {
            options.signal.addEventListener('abort', () => {
              reject(abortError);
            });
          }
        });
      });

      // Use a very short timeout for testing
      const requestPromise = get('/api/slow', { timeout: 10 });

      await expect(requestPromise).rejects.toThrow(ApiError);

      // Reset mock for the second assertion
      global.fetch = vi.fn().mockImplementation((_url, options) => {
        return new Promise((_, reject) => {
          if (options?.signal) {
            options.signal.addEventListener('abort', () => {
              reject(abortError);
            });
          }
        });
      });

      const requestPromise2 = get('/api/slow', { timeout: 10 });

      try {
        await requestPromise2;
        throw new Error('Should have thrown');
      } catch (e) {
        expect(e).toMatchObject({
          status: 0,
          statusText: 'Request timeout',
        });
      }

      // Restore fake timers for other tests
      vi.useFakeTimers();
    });

    it('handles text error body when JSON parsing fails', async () => {
      global.fetch = vi.fn().mockResolvedValue({
        ok: false,
        status: 400,
        statusText: 'Bad Request',
        text: () => Promise.resolve('Plain text error'),
      });

      await expect(get('/api/bad')).rejects.toMatchObject({
        status: 400,
        body: 'Plain text error',
      });
    });

    it('handles JSON error body', async () => {
      const errorBody = { error: 'Bad request', details: 'Invalid field' };
      global.fetch = vi.fn().mockResolvedValue({
        ok: false,
        status: 400,
        statusText: 'Bad Request',
        text: () => Promise.resolve(JSON.stringify(errorBody)),
      });

      await expect(get('/api/bad')).rejects.toMatchObject({
        status: 400,
        body: errorBody,
      });
    });
  });

  describe('Combined signal behavior (AbortSignal.any)', () => {
    it('timeout works when caller provides their own signal', async () => {
      vi.useRealTimers();

      const abortError = new DOMException('The operation was aborted.', 'AbortError');

      global.fetch = vi.fn().mockImplementation((_url, options) => {
        return new Promise((_, reject) => {
          if (options?.signal) {
            options.signal.addEventListener('abort', () => {
              reject(abortError);
            });
          }
        });
      });

      const callerController = new AbortController();
      const requestPromise = get('/api/slow', {
        timeout: 10,
        signal: callerController.signal,
      });

      await expect(requestPromise).rejects.toThrow(ApiError);

      // Verify it was a timeout error, not a caller abort
      global.fetch = vi.fn().mockImplementation((_url, options) => {
        return new Promise((_, reject) => {
          if (options?.signal) {
            options.signal.addEventListener('abort', () => {
              reject(abortError);
            });
          }
        });
      });

      const callerController2 = new AbortController();
      const requestPromise2 = get('/api/slow', {
        timeout: 10,
        signal: callerController2.signal,
      });

      try {
        await requestPromise2;
        throw new Error('Should have thrown');
      } catch (e) {
        expect(e).toMatchObject({
          status: 0,
          statusText: 'Request timeout',
        });
      }

      vi.useFakeTimers();
    });

    it('caller signal abort works when timeout is also configured', async () => {
      vi.useRealTimers();

      const abortError = new DOMException('The operation was aborted.', 'AbortError');

      global.fetch = vi.fn().mockImplementation((_url, options) => {
        return new Promise((_, reject) => {
          if (options?.signal) {
            options.signal.addEventListener('abort', () => {
              reject(abortError);
            });
          }
        });
      });

      const callerController = new AbortController();
      const requestPromise = get('/api/slow', {
        timeout: 5000,
        signal: callerController.signal,
      });

      // Abort from caller before timeout fires
      callerController.abort();

      await expect(requestPromise).rejects.toThrow(DOMException);
      await expect(
        // Need a fresh request for the second assertion
        (async () => {
          global.fetch = vi.fn().mockImplementation((_url, options) => {
            return new Promise((_, reject) => {
              if (options?.signal) {
                options.signal.addEventListener('abort', () => {
                  reject(abortError);
                });
              }
            });
          });
          const ctrl = new AbortController();
          const p = get('/api/slow', { timeout: 5000, signal: ctrl.signal });
          ctrl.abort();
          return p;
        })()
      ).rejects.not.toThrow(ApiError);

      vi.useFakeTimers();
    });

    it('passes combined signal to fetch when caller provides signal', async () => {
      const mockData = { id: 1 };
      global.fetch = vi.fn().mockResolvedValue({
        ok: true,
        status: 200,
        json: () => Promise.resolve(mockData),
      });

      const callerController = new AbortController();
      await get('/api/test', { signal: callerController.signal });

      const mockFn = global.fetch as ReturnType<typeof vi.fn>;
      const call = mockFn.mock.calls[0];
      const options = call?.[1] as { signal: AbortSignal };
      // The signal should NOT be the caller's signal directly (it should be a combined signal)
      expect(options.signal).not.toBe(callerController.signal);
      // But it should still be an AbortSignal
      expect(options.signal).toBeInstanceOf(AbortSignal);
    });
  });

  describe('Custom headers', () => {
    it('can merge custom headers with defaults', async () => {
      global.fetch = vi.fn().mockResolvedValue({
        ok: true,
        status: 200,
        json: () => Promise.resolve({}),
      });

      await get('/api/test', {
        headers: {
          Authorization: 'Bearer token123',
          'X-Custom-Header': 'custom-value',
        },
      });

      const mockFn = global.fetch as ReturnType<typeof vi.fn>;
      const call = mockFn.mock.calls[0];
      expect(call).toBeDefined();
      const options = call?.[1] as { headers: Record<string, string> };
      expect(options.headers).toMatchObject({
        Accept: 'application/json',
        Authorization: 'Bearer token123',
        'X-Custom-Header': 'custom-value',
      });
    });

    it('custom headers can override default Accept header', async () => {
      global.fetch = vi.fn().mockResolvedValue({
        ok: true,
        status: 200,
        json: () => Promise.resolve({}),
      });

      await get('/api/test', {
        headers: {
          Accept: 'text/plain',
        },
      });

      const mockFn = global.fetch as ReturnType<typeof vi.fn>;
      const call = mockFn.mock.calls[0];
      expect(call).toBeDefined();
      const options = call?.[1] as { headers: Record<string, string> };
      expect(options.headers.Accept).toBe('text/plain');
    });
  });
});
