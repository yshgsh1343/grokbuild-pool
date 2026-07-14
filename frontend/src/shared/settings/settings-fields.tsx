import type { ReactNode } from "react";

import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import type { SettingsFormState } from "@/shared/settings/form-utils";
import { cn } from "@/shared/lib/cn";

export function SettingsSection({
  id,
  title,
  note,
  children,
  className,
}: {
  id?: string;
  title: string;
  note?: string;
  children: ReactNode;
  className?: string;
}) {
  return (
    <section
      id={id ? `set-sec-${id}` : undefined}
      className={cn(
        "scroll-mt-20 rounded-lg border border-border bg-card p-4",
        className,
      )}
    >
      <div className="text-sm font-medium tracking-tight">{title}</div>
      {note ? <p className="mt-1 text-xs text-muted-foreground">{note}</p> : null}
      <div className="mt-3 grid gap-3 sm:grid-cols-2 lg:grid-cols-3">{children}</div>
    </section>
  );
}

type SetFn = <K extends keyof SettingsFormState>(
  key: K,
  value: SettingsFormState[K],
) => void;

export function NumField({
  form,
  set,
  label,
  k,
  step,
}: {
  form: SettingsFormState;
  set: SetFn;
  label: string;
  k: keyof SettingsFormState;
  step?: string;
}) {
  return (
    <div className="space-y-1">
      <Label>{label}</Label>
      <Input
        type="number"
        step={step}
        value={Number(form[k] ?? 0)}
        onChange={(e) => set(k, (Number(e.target.value) || 0) as SettingsFormState[typeof k])}
      />
    </div>
  );
}

export function TextField({
  form,
  set,
  label,
  k,
  placeholder,
  type = "text",
}: {
  form: SettingsFormState;
  set: SetFn;
  label: string;
  k: keyof SettingsFormState;
  placeholder?: string;
  type?: "text" | "password";
}) {
  return (
    <div className="space-y-1">
      <Label>{label}</Label>
      <Input
        type={type}
        value={String(form[k] ?? "")}
        placeholder={placeholder}
        onChange={(e) => set(k, e.target.value as SettingsFormState[typeof k])}
      />
    </div>
  );
}

export function BoolField({
  form,
  set,
  label,
  k,
}: {
  form: SettingsFormState;
  set: SetFn;
  label: string;
  k: keyof SettingsFormState;
}) {
  return (
    <div className="flex h-full items-end gap-2 pb-1">
      <Checkbox
        checked={!!form[k]}
        onCheckedChange={(v) => set(k, !!v as SettingsFormState[typeof k])}
        id={String(k)}
      />
      <Label htmlFor={String(k)} className="cursor-pointer">
        {label}
      </Label>
    </div>
  );
}

export function SelectField({
  form,
  set,
  label,
  k,
  options,
}: {
  form: SettingsFormState;
  set: SetFn;
  label: string;
  k: keyof SettingsFormState;
  options: { value: string; label: string }[];
}) {
  return (
    <div className="space-y-1">
      <Label>{label}</Label>
      <Select
        value={String(form[k] ?? "")}
        onValueChange={(v) => set(k, v as SettingsFormState[typeof k])}
      >
        <SelectTrigger>
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {options.map((o) => (
            <SelectItem key={o.value} value={o.value}>
              {o.label}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </div>
  );
}

export function AreaField({
  form,
  set,
  label,
  k,
  rows = 8,
  className,
}: {
  form: SettingsFormState;
  set: SetFn;
  label: string;
  k: keyof SettingsFormState;
  rows?: number;
  className?: string;
}) {
  return (
    <div className={cn("space-y-1 sm:col-span-2", className)}>
      <Label>{label}</Label>
      <Textarea
        rows={rows}
        value={String(form[k] ?? "")}
        onChange={(e) => set(k, e.target.value as SettingsFormState[typeof k])}
        className="mono"
      />
    </div>
  );
}
