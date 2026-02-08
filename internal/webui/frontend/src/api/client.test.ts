import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';

import {
  get,
  post,
  patch,
  del,
  ApiError,
  initAuth,
  getAuthState,
  onAuthStateChange,
  getAuthToken,
} from './client';

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

  describe('Auth', () => {
    // Helper to reset auth state to a known baseline before each auth test.
    // We call initAuth with a 200 mock to set token, or a 404 mock to clear it.
    async function resetAuthState() {
      global.fetch = vi.fn().mockResolvedValue({
        ok: true,
        status: 200,
        json: () => Promise.resolve({ token: 'reset-token' }),
      });
      await initAuth();
    }

    it('initAuth retries on 500', async () => {
      // First call returns 500, second call returns 200 with token
      const mockFetch = vi
        .fn()
        .mockResolvedValueOnce({
          ok: false,
          status: 500,
          statusText: 'Internal Server Error',
          text: () => Promise.resolve('error'),
        })
        .mockResolvedValueOnce({
          ok: true,
          status: 200,
          json: () => Promise.resolve({ token: 'retry-token' }),
        });
      global.fetch = mockFetch;

      const authPromise = initAuth();

      // Advance past the first backoff delay (500ms * 2^0 = 500ms)
      await vi.advanceTimersByTimeAsync(500);

      await authPromise;

      expect(getAuthState()).toBe('authenticated');
      expect(getAuthToken()).toBe('retry-token');
      expect(mockFetch).toHaveBeenCalledTimes(2);
    });

    it('initAuth does NOT retry on 403', async () => {
      const mockFetch = vi.fn().mockResolvedValue({
        ok: false,
        status: 403,
        statusText: 'Forbidden',
        text: () => Promise.resolve('forbidden'),
      });
      global.fetch = mockFetch;

      await initAuth();

      expect(getAuthState()).toBe('disabled');
      expect(mockFetch).toHaveBeenCalledTimes(1);
    });

    it('initAuth does NOT retry on 404', async () => {
      const mockFetch = vi.fn().mockResolvedValue({
        ok: false,
        status: 404,
        statusText: 'Not Found',
        text: () => Promise.resolve('not found'),
      });
      global.fetch = mockFetch;

      await initAuth();

      expect(getAuthState()).toBe('disabled');
    });

    it('initAuth sets state to authenticated on success', async () => {
      global.fetch = vi.fn().mockResolvedValue({
        ok: true,
        status: 200,
        json: () => Promise.resolve({ token: 'test-token' }),
      });

      await initAuth();

      expect(getAuthState()).toBe('authenticated');
      expect(getAuthToken()).toBe('test-token');
    });

    it('initAuth sets state to failed after all retries exhausted', async () => {
      global.fetch = vi.fn().mockRejectedValue(new TypeError('Failed to fetch'));

      const authPromise = initAuth({ maxRetries: 1 });

      // attempt 0 fails -> backoff 500ms
      await vi.advanceTimersByTimeAsync(500);
      // attempt 1 fails -> exhausted

      await authPromise;

      expect(getAuthState()).toBe('failed');
    });

    it('fetchApi 401 interceptor - re-auth succeeds', async () => {
      // First, establish an authenticated state
      await resetAuthState();

      const mockFetch = vi.fn();

      // Call 1: the API request returns 401
      mockFetch.mockResolvedValueOnce({
        ok: false,
        status: 401,
        statusText: 'Unauthorized',
        text: () => Promise.resolve('unauthorized'),
      });

      // Call 2: the re-auth call to /api/auth/token returns new token
      mockFetch.mockResolvedValueOnce({
        ok: true,
        status: 200,
        json: () => Promise.resolve({ token: 'new-token' }),
      });

      // Call 3: the retried API request succeeds
      mockFetch.mockResolvedValueOnce({
        ok: true,
        status: 200,
        json: () => Promise.resolve({ data: 'success' }),
      });

      global.fetch = mockFetch;

      const result = await get<{ data: string }>('/api/test');

      expect(result).toEqual({ data: 'success' });
      expect(mockFetch).toHaveBeenCalledTimes(3);

      // Verify the calls in order:
      // 1st: GET /api/test (returns 401)
      expect(mockFetch.mock.calls[0][0]).toBe('/api/test');
      // 2nd: GET /api/auth/token (re-auth)
      expect(mockFetch.mock.calls[1][0]).toBe('/api/auth/token');
      // 3rd: GET /api/test (retry)
      expect(mockFetch.mock.calls[2][0]).toBe('/api/test');
    });

    it('fetchApi 401 interceptor - re-auth fails', async () => {
      // Establish authenticated state
      await resetAuthState();

      const mockFetch = vi.fn();

      // Call 1: the API request returns 401
      mockFetch.mockResolvedValueOnce({
        ok: false,
        status: 401,
        statusText: 'Unauthorized',
        text: () => Promise.resolve('unauthorized'),
      });

      // Call 2: re-auth fails (e.g. 500)
      mockFetch.mockResolvedValueOnce({
        ok: false,
        status: 500,
        statusText: 'Internal Server Error',
        text: () => Promise.resolve('error'),
      });

      global.fetch = mockFetch;

      await expect(get('/api/test')).rejects.toThrow(ApiError);
      await resetAuthState();
      // Reset again for clean state, then re-test to check the error shape
      const mockFetch2 = vi.fn();
      mockFetch2.mockResolvedValueOnce({
        ok: false,
        status: 401,
        statusText: 'Unauthorized',
        text: () => Promise.resolve('unauthorized'),
      });
      mockFetch2.mockResolvedValueOnce({
        ok: false,
        status: 500,
        statusText: 'Internal Server Error',
        text: () => Promise.resolve('error'),
      });
      global.fetch = mockFetch2;

      await expect(get('/api/test')).rejects.toMatchObject({
        status: 401,
        statusText: 'Unauthorized',
      });
    });

    it('fetchApi 401 does not retry infinitely', async () => {
      // Establish authenticated state
      await resetAuthState();

      const mockFetch = vi.fn();

      // Call 1: API request returns 401
      mockFetch.mockResolvedValueOnce({
        ok: false,
        status: 401,
        statusText: 'Unauthorized',
        text: () => Promise.resolve('unauthorized'),
      });

      // Call 2: re-auth succeeds with new token
      mockFetch.mockResolvedValueOnce({
        ok: true,
        status: 200,
        json: () => Promise.resolve({ token: 'new-token-2' }),
      });

      // Call 3: retried API request ALSO returns 401
      mockFetch.mockResolvedValueOnce({
        ok: false,
        status: 401,
        statusText: 'Unauthorized',
        text: () => Promise.resolve('still unauthorized'),
      });

      global.fetch = mockFetch;

      await expect(get('/api/test')).rejects.toThrow(ApiError);

      // Reset and re-test for error shape
      await resetAuthState();
      const mockFetch2 = vi.fn();
      mockFetch2.mockResolvedValueOnce({
        ok: false,
        status: 401,
        statusText: 'Unauthorized',
        text: () => Promise.resolve('unauthorized'),
      });
      mockFetch2.mockResolvedValueOnce({
        ok: true,
        status: 200,
        json: () => Promise.resolve({ token: 'new-token-3' }),
      });
      mockFetch2.mockResolvedValueOnce({
        ok: false,
        status: 401,
        statusText: 'Unauthorized',
        text: () => Promise.resolve('still unauthorized'),
      });
      global.fetch = mockFetch2;

      await expect(get('/api/test')).rejects.toMatchObject({
        status: 401,
      });

      // Verify it didn't loop - should be exactly 3 calls (original, re-auth, retry)
      expect(mockFetch2).toHaveBeenCalledTimes(3);
    });

    it('concurrent initAuth calls are deduplicated', async () => {
      const mockFetch = vi.fn().mockResolvedValue({
        ok: true,
        status: 200,
        json: () => Promise.resolve({ token: 'dedup-token' }),
      });
      global.fetch = mockFetch;

      // Call initAuth twice without awaiting the first
      const promise1 = initAuth();
      const promise2 = initAuth();

      await Promise.all([promise1, promise2]);

      expect(getAuthState()).toBe('authenticated');
      // fetch should only have been called once due to deduplication
      expect(mockFetch).toHaveBeenCalledTimes(1);
    });

    it('onAuthStateChange fires on state transitions', async () => {
      // First set a non-authenticated state so that transitioning to 'authenticated'
      // actually triggers the listener
      global.fetch = vi.fn().mockResolvedValue({
        ok: false,
        status: 404,
        statusText: 'Not Found',
        text: () => Promise.resolve('not found'),
      });
      await initAuth();
      expect(getAuthState()).toBe('disabled');

      const callback = vi.fn();
      const unsubscribe = onAuthStateChange(callback);

      try {
        global.fetch = vi.fn().mockResolvedValue({
          ok: true,
          status: 200,
          json: () => Promise.resolve({ token: 'listener-token' }),
        });

        await initAuth();

        expect(callback).toHaveBeenCalledWith('authenticated');
        expect(callback).toHaveBeenCalledTimes(1);
      } finally {
        unsubscribe();
      }
    });
  });
});
