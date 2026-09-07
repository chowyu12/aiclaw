export type ModelProvider = {
  id: number;
  models: string[];
};

export type ModelSelection = {
  provider_id: number;
  model_name: string;
};

export function resolveModelSelection(
  providers: ModelProvider[],
  current: ModelSelection,
  saved?: ModelSelection,
): ModelSelection {
  for (const candidate of [saved, current]) {
    if (!candidate?.provider_id || !candidate.model_name) continue;
    const provider = providers.find(
      (item) => item.id === candidate.provider_id,
    );
    if (provider?.models.includes(candidate.model_name)) return candidate;
  }

  const fallback = providers.find((item) => item.models.length > 0);
  return fallback
    ? { provider_id: fallback.id, model_name: fallback.models[0] }
    : { provider_id: 0, model_name: "" };
}
