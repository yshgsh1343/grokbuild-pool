import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import { api, ApiError } from "@/shared/api/client";
import type { RuntimeSettings, SettingsSnapshot } from "@/shared/api/types";
import { ErrorState, LoadingState } from "@/shared/components/data-state";
import { PageHeader } from "@/shared/components/page-header";

const STRIP_ON_SAVE = new Set([
  "persisted_path",
  "restart_hint",
  "api_key",
  "admin_key",
  "import_sso_api_key",
  "api_key_configured",
  "admin_key_configured",
  "import_sso_api_key_set",
]);

export function JsonSettingsPage() {
  const qc = useQueryClient();
  const [text, setText] = useState("");
  const [path, setPath] = useState("");

  const q = useQuery({
    queryKey: ["settings"],
    queryFn: () => api<SettingsSnapshot>("/admin/settings"),
  });

  useEffect(() => {
    if (q.data) {
      setText(JSON.stringify(q.data, null, 2));
      setPath(q.data.persisted_path || "");
    }
  }, [q.data]);

  const saveM = useMutation({
    mutationFn: (body: RuntimeSettings) =>
      api<{ ok: boolean; persisted: boolean; settings: SettingsSnapshot }>(
        "/admin/settings",
        { method: "PUT", body },
      ),
    onSuccess: (res) => {
      const s = res.settings || {};
      setText(JSON.stringify({ ...s, persisted_path: s.persisted_path || path }, null, 2));
      if (s.persisted_path) setPath(s.persisted_path);
      if (s.restart_hint) toast.warning(s.restart_hint);
      else toast.success(res.persisted ? "JSON 已保存并热更新" : "JSON 已应用");
      void qc.invalidateQueries({ queryKey: ["settings"] });
    },
    onError: (e: Error) => toast.error(e.message),
  });

  function onSave() {
    let parsed: Record<string, unknown>;
    try {
      parsed = JSON.parse(text) as Record<string, unknown>;
    } catch (e) {
      toast.error("JSON 解析失败：" + (e instanceof Error ? e.message : String(e)));
      return;
    }
    if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
      toast.error("根节点必须是对象");
      return;
    }
    const body: Record<string, unknown> = { ...parsed };
    for (const k of STRIP_ON_SAVE) delete body[k];
    // empty secrets = do not send
    for (const k of ["api_key", "admin_key", "import_sso_api_key"]) {
      if (body[k] === "" || body[k] == null) delete body[k];
    }
    saveM.mutate(body as RuntimeSettings);
  }

  if (q.isPending && !text) return <LoadingState />;
  if (q.isError && !text) {
    return (
      <ErrorState
        message={q.error instanceof ApiError ? q.error.message : "加载失败"}
        onRetry={() => void q.refetch()}
      />
    );
  }

  return (
    <div className="space-y-4">
      <PageHeader
        title="JSON 配置"
        description="查看并直接编辑运行时 settings 快照。保存走 PUT /admin/settings（整表合并语义）。密钥字段留空或不传表示不修改。"
        actions={
          <>
            <Button
              variant="secondary"
              size="sm"
              onClick={() => void q.refetch()}
              disabled={q.isFetching}
            >
              重新加载
            </Button>
            <Button
              variant="secondary"
              size="sm"
              onClick={() => {
                void navigator.clipboard.writeText(text);
                toast.success("已复制");
              }}
            >
              复制
            </Button>
            <Button size="sm" disabled={saveM.isPending} onClick={onSave}>
              {saveM.isPending ? "保存中…" : "保存 JSON"}
            </Button>
          </>
        }
      />
      <Textarea
        value={text}
        onChange={(e) => setText(e.target.value)}
        className="mono min-h-[min(70vh,720px)] text-[12px] leading-relaxed"
        spellCheck={false}
      />
    </div>
  );
}
