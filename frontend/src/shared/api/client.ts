export class ApiError extends Error {
  status: number;

  constructor(message: string, status: number) {
    super(message);
    this.name = "ApiError";
    this.status = status;
  }
}

type AdminKeyGetter = () => string | null;
type UnauthHandler = () => void;

let getAdminKey: AdminKeyGetter = () => null;
let onUnauth: UnauthHandler | null = null;

export function configureApi(opts: {
  getAdminKey: AdminKeyGetter;
  onUnauth?: UnauthHandler;
}) {
  getAdminKey = opts.getAdminKey;
  onUnauth = opts.onUnauth ?? null;
}

function authHeaders(extra?: HeadersInit, dual = false): Headers {
  const h = new Headers(extra);
  const key = getAdminKey();
  if (key) {
    h.set("Authorization", `Bearer ${key}`);
    if (dual) h.set("X-Admin-Key", key);
  }
  if (!h.has("Accept")) h.set("Accept", "application/json");
  return h;
}

async function parseJsonSafe(text: string): Promise<unknown> {
  if (!text) return {};
  try {
    return JSON.parse(text) as unknown;
  } catch {
    return {};
  }
}

function errorMessage(body: unknown, status: number): string {
  if (body && typeof body === "object" && "error" in body) {
    const err = (body as { error?: unknown }).error;
    if (typeof err === "string" && err) return err;
  }
  return `HTTP ${status}`;
}

export type ApiOptions = {
  method?: string;
  body?: unknown;
  dualAuth?: boolean;
  signal?: AbortSignal;
};

export async function api<T = unknown>(path: string, opts: ApiOptions = {}): Promise<T> {
  const isForm = typeof FormData !== "undefined" && opts.body instanceof FormData;
  const headers = authHeaders(undefined, opts.dualAuth);
  if (!isForm && opts.body !== undefined) {
    headers.set("Content-Type", "application/json");
  }

  const res = await fetch(path, {
    method: opts.method ?? "GET",
    headers,
    body:
      opts.body === undefined
        ? undefined
        : isForm
          ? (opts.body as FormData)
          : JSON.stringify(opts.body),
    signal: opts.signal,
  });

  const text = await res.text();
  const json = await parseJsonSafe(text);

  if (!res.ok) {
    const err = new ApiError(errorMessage(json, res.status), res.status);
    if (res.status === 401 && onUnauth) onUnauth();
    throw err;
  }

  return json as T;
}

export async function apiBlob(
  path: string,
  opts: { dualAuth?: boolean; signal?: AbortSignal } = {},
): Promise<{ blob: Blob; filename: string | null }> {
  const headers = authHeaders(undefined, opts.dualAuth ?? true);
  const res = await fetch(path, {
    method: "GET",
    headers,
    signal: opts.signal,
  });
  if (!res.ok) {
    const text = await res.text();
    const json = await parseJsonSafe(text);
    const err = new ApiError(errorMessage(json, res.status), res.status);
    if (res.status === 401 && onUnauth) onUnauth();
    throw err;
  }
  const cd = res.headers.get("Content-Disposition") ?? "";
  const m = /filename\*?=(?:UTF-8''|")?([^\";]+)/i.exec(cd);
  const filename = m ? decodeURIComponent(m[1].replace(/"/g, "")) : null;
  return { blob: await res.blob(), filename };
}
