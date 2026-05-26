import { useMemo } from "react";
import type { AstralEvent } from "../types";

type ContextUsage = {
  totalTokens?: number;
  modelContextWindow?: number;
  usedPercent?: number;
};

type ModelOption = {
  id: string;
  slot?: string;
  context_window?: number;
  effective_context_window?: number;
};

export function useContextUsage(
  events: AstralEvent[],
  models: ModelOption[],
  modelValue: string,
  slotValue: string,
  currentModel?: string,
): ContextUsage | undefined {
  const modelContextWindow = useMemo(
    () => selectedModelContextWindow(models, modelValue, slotValue, currentModel),
    [currentModel, modelValue, models, slotValue],
  );

  return useMemo(() => {
    const latest = latestContextUsage(events);
    if (latest) return latest;
    return modelContextWindow ? { modelContextWindow } : undefined;
  }, [events, modelContextWindow]);
}

function latestContextUsage(events: AstralEvent[]): ContextUsage | undefined {
  for (let index = events.length - 1; index >= 0; index--) {
    const event = events[index];
    if (event.kind !== "control.context") continue;
    const value = event.normalized as Record<string, unknown>;
    const totalTokens = numberValue(value.total_tokens);
    const modelContextWindow = numberValue(value.model_context_window);
    const usedPercent = numberValue(value.used_percent) || (totalTokens && modelContextWindow ? Math.max(1, Math.round((totalTokens / modelContextWindow) * 100)) : undefined);
    return {
      totalTokens,
      modelContextWindow,
      usedPercent,
    };
  }
  return undefined;
}

function selectedModelContextWindow(models: ModelOption[], modelValue: string, slotValue: string, currentModel?: string): number | undefined {
  const model = slotValue
    ? models.find((item) => item.slot === slotValue)
    : modelValue
      ? models.find((item) => item.id === modelValue && !item.slot) ?? models.find((item) => item.id === modelValue)
      : currentModel
        ? models.find((item) => item.id === currentModel)
        : models[0];
  return numberValue(model?.effective_context_window) || numberValue(model?.context_window);
}

function numberValue(value: unknown): number | undefined {
  return typeof value === "number" && Number.isFinite(value) ? value : undefined;
}
