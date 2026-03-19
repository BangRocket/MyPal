// Copyright (c) MyPal contributors. See LICENSE for details.

import type { Component } from "solid-js";
import { createSignal, For, Show, onMount } from "solid-js";
import { A } from "@solidjs/router";
import { MODEL_TIERS_QUERY } from "@mypal/ui/graphql/queries";
import { GRAPHQL_ENDPOINT } from "../../graphql/client";
import { getStoredToken, setNeedsAuth } from "../../stores/authStore";
import AppShell from "../../components/AppShell";
import "./ModelTiersView.css";

interface ModelTier {
  name: string;
  provider: string;
  model: string;
  prefix: string;
  costCap: number;
  isDefault: boolean;
}

interface ModelTiersConfig {
  enabled: boolean;
  tiers: ModelTier[];
}

function graphqlHeaders(): Record<string, string> {
  const headers: Record<string, string> = { "Content-Type": "application/json" };
  const token = getStoredToken();
  if (token) headers["Authorization"] = `Bearer ${token}`;
  return headers;
}

const ModelTiersView: Component = () => {
  const [config, setConfig] = createSignal<ModelTiersConfig | null>(null);
  const [isLoading, setIsLoading] = createSignal(true);
  const [error, setError] = createSignal<string | null>(null);

  onMount(async () => {
    const controller = new AbortController();
    const timeoutId = setTimeout(() => controller.abort(), 10000);

    try {
      const res = await fetch(GRAPHQL_ENDPOINT, {
        method: "POST",
        signal: controller.signal,
        headers: graphqlHeaders(),
        body: JSON.stringify({ query: MODEL_TIERS_QUERY }),
      });

      if (res.status === 401) {
        setNeedsAuth(true);
        return;
      }

      const data = await res.json();

      if (data.errors) {
        setError(data.errors[0]?.message ?? "Failed to load model tiers");
        return;
      }

      setConfig(data.data?.modelTiers ?? null);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to load model tiers");
    } finally {
      clearTimeout(timeoutId);
      setIsLoading(false);
    }
  });

  return (
    <AppShell activeTab="settings">
      <Show when={isLoading()}>
        <div class="model-tiers-loading">
          <span class="material-symbols-outlined model-tiers-loading__icon">tune</span>
          <p class="model-tiers-loading__title">Loading model tiers...</p>
        </div>
      </Show>

      <Show when={!isLoading()}>
        <div class="model-tiers-view">
          <div class="model-tiers-header">
            <div>
              <A href="/settings" class="model-tiers-header__back">
                <span class="material-symbols-outlined">arrow_back</span>
                Settings
              </A>
              <h1>Model Tiers</h1>
            </div>
            <Show when={config()}>
              <span
                class="model-tiers-status"
                classList={{
                  "model-tiers-status--enabled": config()!.enabled,
                  "model-tiers-status--disabled": !config()!.enabled,
                }}
              >
                {config()!.enabled ? "Enabled" : "Disabled"}
              </span>
            </Show>
          </div>

          <Show when={error()}>
            <div class="model-tiers-error">{error()}</div>
          </Show>

          <Show when={config() && config()!.tiers.length > 0}>
            <section>
              <table class="model-tiers-table">
                <thead>
                  <tr>
                    <th>Name</th>
                    <th>Provider</th>
                    <th>Model</th>
                    <th>Prefix</th>
                    <th>Cost Cap</th>
                    <th>Default</th>
                  </tr>
                </thead>
                <tbody>
                  <For each={config()!.tiers}>
                    {(tier) => (
                      <tr>
                        <td>{tier.name}</td>
                        <td>{tier.provider}</td>
                        <td>{tier.model}</td>
                        <td>
                          <Show when={tier.prefix} fallback={<span style={{ color: "var(--color-text-muted)" }}>&mdash;</span>}>
                            <code class="model-tiers-table__prefix">{tier.prefix}</code>
                          </Show>
                        </td>
                        <td>
                          <Show when={tier.costCap > 0} fallback={<span style={{ color: "var(--color-text-muted)" }}>&mdash;</span>}>
                            ${tier.costCap.toFixed(4)}
                          </Show>
                        </td>
                        <td>
                          <Show when={tier.isDefault}>
                            <span class="model-tiers-table__default-badge">Default</span>
                          </Show>
                        </td>
                      </tr>
                    )}
                  </For>
                </tbody>
              </table>
            </section>
          </Show>

          <Show when={config() && config()!.tiers.length === 0}>
            <div class="model-tiers-empty">
              No model tiers configured.
            </div>
          </Show>

          <div class="model-tiers-note">
            <span class="material-symbols-outlined">info</span>
            <span>
              Model tier configuration is managed via <strong>mypal.yaml</strong> or environment variables.
            </span>
          </div>
        </div>
      </Show>

      <footer class="app-shell__footer">MyPal</footer>
    </AppShell>
  );
};

export default ModelTiersView;
