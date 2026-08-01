export class APIError extends Error {
  status: number;
  code?: string;

  constructor(message: string, status: number, code?: string) {
    super(message);
    this.name = 'APIError';
    this.status = status;
    this.code = code;
  }
}

export async function api<T>(endpoint: string, options: RequestInit = {}): Promise<T> {
  const headers = new Headers(options.headers || {});
  if (options.body) headers.set('Content-Type', 'application/json');
  const response = await fetch(endpoint, { ...options, headers, credentials: 'same-origin' });
  if (response.status === 401 && !endpoint.startsWith('/api/me')) {
    const returnTo = safeReturnTo(`${location.pathname}${location.search}${location.hash}`);
    location.href = `/login?return_to=${encodeURIComponent(returnTo)}`;
    throw new APIError('需要登录', 401);
  }
  if (!response.ok) {
    const problem = (await response.json().catch(() => ({}))) as { error?: { code?: string; message?: string } };
    throw new APIError(problem.error?.message || `请求失败 (${response.status})`, response.status, problem.error?.code);
  }
  return response.status === 204 ? (undefined as T) : ((await response.json()) as T);
}

export function safeReturnTo(value: string | null | undefined): string {
  if (!value) return '/';
  const returnTo = String(value);
  if (returnTo.startsWith('//') || returnTo.startsWith('http://') || returnTo.startsWith('https://')) return '/';
  if (!returnTo.startsWith('/')) return '/';
  return returnTo;
}
