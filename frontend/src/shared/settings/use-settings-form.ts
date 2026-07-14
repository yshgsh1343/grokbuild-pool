import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { toast } from "sonner";

import { api } from "@/shared/api/client";
import type { RuntimeSettings, SettingsSnapshot } from "@/shared/api/types";
import {
  fromSnapshot,
  mergePatchBody,
  toBody,
  type SettingsFormState,
} from "@/shared/settings/form-utils";

export function useSettingsForm() {
  const qc = useQueryClient();
  const [form, setForm] = useState<SettingsFormState | null>(null);
  const [meta, setMeta] = useState<SettingsSnapshot | null>(null);

  const q = useQuery({
    queryKey: ["settings"],
    queryFn: () => api<SettingsSnapshot>("/admin/settings"),
  });

  useEffect(() => {
    if (q.data) {
      setForm(fromSnapshot(q.data));
      setMeta(q.data);
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
      const merged: SettingsSnapshot = {
        ...s,
        persisted_path: s.persisted_path || meta?.persisted_path,
      };
      setMeta(merged);
      setForm((f) =>
        f
          ? {
              ...f,
              import_sso_api_key: "",
              api_key: "",
              admin_key: "",
            }
          : f,
      );
      if (merged.restart_hint) {
        toast.warning((res.persisted ? "已保存。" : "已应用。") + merged.restart_hint);
      } else {
        toast.success(res.persisted ? "已保存并热更新（无需重启）" : "已热更新（无需重启）");
      }
      void qc.invalidateQueries({ queryKey: ["settings"] });
    },
    onError: (e: Error) => toast.error(e.message),
  });

  function set<K extends keyof SettingsFormState>(key: K, value: SettingsFormState[K]) {
    setForm((f) => (f ? { ...f, [key]: value } : f));
  }

  function saveFull() {
    if (!form) return;
    saveM.mutate(toBody(form));
  }

  /** Save only keys in patch; rest taken from latest server snapshot. */
  function savePatch(patch: Partial<SettingsFormState>) {
    const base = meta || q.data;
    if (!base) {
      toast.error("设置尚未加载");
      return;
    }
    // merge current form secrets/local edits for patch keys
    const fromForm: Partial<SettingsFormState> = {};
    if (form) {
      for (const k of Object.keys(patch) as (keyof SettingsFormState)[]) {
        fromForm[k] = form[k] as never;
      }
    }
    saveM.mutate(mergePatchBody(base, { ...fromForm, ...patch }));
  }

  return {
    form,
    setForm,
    set,
    meta,
    query: q,
    saveMutation: saveM,
    saveFull,
    savePatch,
    reload: () => void q.refetch(),
  };
}
