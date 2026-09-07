import { describe, expect, it } from "vitest";
import { resolveModelSelection } from "./model-selection";

const providers = [
  { id: 1, models: ["model-a", "model-b"] },
  { id: 2, models: ["model-c"] },
];

describe("resolveModelSelection", () => {
  it("restores the last valid model instead of the first model", () => {
    expect(
      resolveModelSelection(
        providers,
        { provider_id: 0, model_name: "" },
        { provider_id: 1, model_name: "model-b" },
      ),
    ).toEqual({ provider_id: 1, model_name: "model-b" });
  });

  it("falls back when the stored model was removed", () => {
    expect(
      resolveModelSelection(
        providers,
        { provider_id: 0, model_name: "" },
        { provider_id: 1, model_name: "removed" },
      ),
    ).toEqual({ provider_id: 1, model_name: "model-a" });
  });

  it("keeps the current model during an ordinary refresh", () => {
    expect(
      resolveModelSelection(providers, {
        provider_id: 2,
        model_name: "model-c",
      }),
    ).toEqual({ provider_id: 2, model_name: "model-c" });
  });
});
