const API_BASE_URL = '';
const DEFAULT_TIMEOUT = 30000;

export class ApiError extends Error {
  constructor(
    public status: number,
    public statusText: string,
    public body?: unknown
  ) {
    super(`API Error: ${status} ${statusText}`);
    this.name = 'ApiError';
  }
}

export type RequestOptions = {
  headers?: Record<string, string>;
  timeout?: number;
  signal?: AbortSignal;
};

async function fetchApi<T>(
  method: string,
  path: string,
  body?: unknown,
  options: RequestOptions = {}
): Promise<T> {
  const controller = new AbortController();
  const timeout = options.timeout ?? DEFAULT_TIMEOUT;
  let timedOut = false;

  const timeoutId = setTimeout(() => {
    timedOut = true;
    controller.abort();
  }, timeout);

  const clearTimeoutCleanup = () => clearTimeout(timeoutId);

  const signal = options.signal
    ? AbortSignal.any([controller.signal, options.signal])
    : controller.signal;

  try {
    const headers: Record<string, string> = {
      Accept: 'application/json',
      ...options.headers,
    };

    if (body !== undefined) {
      headers['Content-Type'] = 'application/json';
    }

    const requestBody = body !== undefined ? JSON.stringify(body) : null;

    const response = await fetch(`${API_BASE_URL}${path}`, {
      method,
      headers,
      body: requestBody,
      signal,
    });

    clearTimeoutCleanup();

    if (!response.ok) {
      let errorBody: unknown;
      const responseText = await response.text();
      try {
        errorBody = JSON.parse(responseText);
      } catch {
        errorBody = responseText;
      }
      throw new ApiError(response.status, response.statusText, errorBody);
    }

    if (response.status === 204) {
      return undefined as T;
    }

    return (await response.json()) as T;
  } catch (error) {
    clearTimeoutCleanup();
    if (error instanceof ApiError) throw error;
    if (error instanceof DOMException && error.name === 'AbortError') {
      if (timedOut) {
        throw new ApiError(0, 'Request timeout');
      }
      // User-provided signal was aborted - re-throw as-is
      throw error;
    }
    throw new ApiError(0, 'Network error', error);
  }
}

export const get = <T>(path: string, options?: RequestOptions): Promise<T> =>
  fetchApi<T>('GET', path, undefined, options);

export const post = <T>(path: string, body: unknown, options?: RequestOptions): Promise<T> =>
  fetchApi<T>('POST', path, body, options);

export const patch = <T>(path: string, body: unknown, options?: RequestOptions): Promise<T> =>
  fetchApi<T>('PATCH', path, body, options);

export const del = <T>(path: string, options?: RequestOptions): Promise<T> =>
  fetchApi<T>('DELETE', path, undefined, options);
