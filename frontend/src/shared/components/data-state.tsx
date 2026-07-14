import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";

export function LoadingState({ label = "加载中…" }: { label?: string }) {
  return (
    <div className="flex items-center justify-center gap-2 py-16 text-xs text-muted-foreground">
      <Spinner />
      {label}
    </div>
  );
}

export function EmptyState({
  title = "暂无数据",
  description,
}: {
  title?: string;
  description?: string;
}) {
  return (
    <div className="px-4 py-14 text-center">
      <div className="text-sm font-medium">{title}</div>
      {description ? <p className="mt-1 text-xs text-muted-foreground">{description}</p> : null}
    </div>
  );
}

export function ErrorState({
  message,
  onRetry,
}: {
  message: string;
  onRetry?: () => void;
}) {
  return (
    <div className="rounded-lg border border-destructive/30 bg-destructive/5 px-4 py-6 text-center">
      <p className="text-xs text-destructive">{message}</p>
      {onRetry ? (
        <Button variant="secondary" size="sm" className="mt-3" onClick={onRetry}>
          重试
        </Button>
      ) : null}
    </div>
  );
}
