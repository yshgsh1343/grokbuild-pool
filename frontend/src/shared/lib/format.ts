export function formatNumber(n: number | null | undefined, digits = 0): string {
  if (n == null || Number.isNaN(Number(n))) return "—";
  return Number(n).toLocaleString("zh-CN", {
    maximumFractionDigits: digits,
    minimumFractionDigits: digits > 0 ? Math.min(digits, 1) : 0,
  });
}

export function formatPercent(rate: number | null | undefined, digits = 1): string {
  if (rate == null || Number.isNaN(Number(rate))) return "—";
  return `${(Number(rate) * 100).toFixed(digits)}%`;
}

export function formatUnix(sec: number | null | undefined): string {
  const n = Number(sec);
  if (!n) return "—";
  try {
    return new Date(n * 1000).toLocaleString("zh-CN");
  } catch {
    return String(sec);
  }
}

export function formatDuration(sec: number | null | undefined): string {
  const n = Math.max(0, Math.floor(Number(sec) || 0));
  if (n < 60) return `${n}s`;
  if (n < 3600) return `${Math.ceil(n / 60)}m`;
  if (n < 86400) return `${Math.ceil(n / 3600)}h`;
  return `${Math.ceil(n / 86400)}d`;
}

export function formatBytes(n: number | null | undefined): string {
  const v = Number(n) || 0;
  if (v < 1024) return `${v} B`;
  if (v < 1024 * 1024) return `${(v / 1024).toFixed(1)} KB`;
  if (v < 1024 * 1024 * 1024) return `${(v / 1024 / 1024).toFixed(1)} MB`;
  return `${(v / 1024 / 1024 / 1024).toFixed(2)} GB`;
}

export async function copyText(text: string): Promise<void> {
  if (navigator.clipboard?.writeText) {
    await navigator.clipboard.writeText(text);
    return;
  }
  const ta = document.createElement("textarea");
  ta.value = text;
  ta.style.position = "fixed";
  ta.style.left = "-9999px";
  document.body.appendChild(ta);
  ta.select();
  document.execCommand("copy");
  document.body.removeChild(ta);
}
