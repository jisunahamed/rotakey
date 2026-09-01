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

/**
 * Every admin call carries the same session cookie and the same CSRF header,
 * and every 401 means the same thing. Both api() and apiStream() go through
 * here so a change to how the console authenticates cannot reach one of them
 * and miss the other.
 */
function adminHeaders(method: string, source?: HeadersInit): Headers {
  const headers = new Headers(source);
  if (!["GET", "HEAD", "OPTIONS"].includes(method) && csrfToken) {
    headers.set("X-CSRF-Token", csrfToken);
  }
  return headers;
}

async function failure(response: Response, path: string): Promise<APIError> {
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
  return new APIError(message, response.status, code);
}

export async function api<T>(
  path: string,
  options: RequestInit & { json?: unknown } = {}
): Promise<T> {
  const method = (options.method ?? "GET").toUpperCase();
  const headers = adminHeaders(method, options.headers);
  if (options.json !== undefined) {
    headers.set("Content-Type", "application/json");
  }
  const response = await fetch(path, {
    ...options,
    headers,
    credentials: "same-origin",
    body: options.json === undefined ? options.body : JSON.stringify(options.json)
  });
  if (!response.ok) {
    throw await failure(response, path);
  }
  if (response.status === 204) {
    return undefined as T;
  }
  return (await response.json()) as T;
}

/**
 * The streaming sibling of api(). It returns the Response itself rather than a
 * parsed body, because the caller reads it a chunk at a time.
 *
 * EventSource cannot be used for this: the admin API requires a POST and an
 * X-CSRF-Token header, and EventSource can send neither. fetch can, at the cost
 * of parsing the event stream ourselves — which lib/stream.ts does.
 *
 * A failure before the first byte still arrives as an ordinary JSON error, so
 * it is raised as an APIError exactly as it would be from api(). A failure
 * after that arrives inside the stream and is the reader's to handle.
 */
export async function apiStream(
  path: string,
  json: unknown,
  signal?: AbortSignal
): Promise<Response> {
  const headers = adminHeaders("POST");
  headers.set("Content-Type", "application/json");
  headers.set("Accept", "text/event-stream");
  const response = await fetch(path, {
    method: "POST",
    headers,
    credentials: "same-origin",
    body: JSON.stringify(json),
    signal
  });
  if (!response.ok) {
    throw await failure(response, path);
  }
  return response;
}
