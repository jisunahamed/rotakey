let csrfToken = "";

export class APIError extends Error {
  status: number;
  code: string;

  constructor(message: string, status: number, code = "request_failed") {
    super(message);
    this.status = status;
    this.code = code;
  }
}

export function setCSRF(value: string) {
  csrfToken = value;
}

export async function api<T>(
  path: string,
  options: RequestInit & { json?: unknown } = {}
): Promise<T> {
  const headers = new Headers(options.headers);
  if (options.json !== undefined) {
    headers.set("Content-Type", "application/json");
  }
  const method = (options.method ?? "GET").toUpperCase();
  if (!["GET", "HEAD", "OPTIONS"].includes(method) && csrfToken) {
    headers.set("X-CSRF-Token", csrfToken);
  }
  const response = await fetch(path, {
    ...options,
    headers,
    credentials: "same-origin",
    body: options.json === undefined ? options.body : JSON.stringify(options.json)
  });
  if (!response.ok) {
    let message = `Request failed (${response.status})`;
    let code = "request_failed";
    try {
      const payload = await response.json();
      message = payload?.error?.message ?? message;
      code = payload?.error?.code ?? code;
    } catch {
      // The status remains the useful error when an upstream returns non-JSON.
    }
    if (response.status === 401 && path.startsWith("/api/admin")) {
      window.dispatchEvent(new Event("relay:session-expired"));
    }
    throw new APIError(message, response.status, code);
  }
  if (response.status === 204) {
    return undefined as T;
  }
  return (await response.json()) as T;
}
