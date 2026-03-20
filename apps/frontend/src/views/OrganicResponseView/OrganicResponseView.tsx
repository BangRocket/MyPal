// Copyright (c) MyPal contributors. See LICENSE for details.

import type { Component } from "solid-js";
import { createSignal, Show, For } from "solid-js";
import { createQuery, useQueryClient } from "@tanstack/solid-query";
import { ORGANIC_CONFIG_QUERY } from "@mypal/ui/graphql/queries";
import { UPDATE_ORGANIC_CONFIG_MUTATION } from "@mypal/ui/graphql/mutations";
import { CHANNELS_QUERY } from "@mypal/ui/graphql/queries";
import { client } from "../../graphql/client";
import AppShell from "../../components/AppShell";
import "./OrganicResponseView.css";

// ── Types ────────────────────────────────────────────────────────────────────

interface OrganicResponseConfig {
  channelId: string;
  enabled: boolean;
  cooldownSeconds: number;
  relevanceThreshold: number;
  maxDailyOrganic: number;
  allowReactions: boolean;
  threadPolicy: string;
  quietHoursStart: string | null;
  quietHoursEnd: string | null;
}

interface Channel {
  id: string;
  name: string;
  type: string;
  status: string;
}

// ── Defaults ─────────────────────────────────────────────────────────────────

function defaultConfig(channelId: string): OrganicResponseConfig {
  return {
    channelId,
    enabled: false,
    cooldownSeconds: 300,
    relevanceThreshold: 0.7,
    maxDailyOrganic: 10,
    allowReactions: true,
    threadPolicy: "joined_only",
    quietHoursStart: null,
    quietHoursEnd: null,
  };
}

// ── Component ────────────────────────────────────────────────────────────────

const OrganicResponseView: Component = () => {
  const queryClient = useQueryClient();

  // Channel selection
  const [channelId, setChannelId] = createSignal("");
  const [selectedChannel, setSelectedChannel] = createSignal("");

  // Form state
  const [enabled, setEnabled] = createSignal(false);
  const [cooldown, setCooldown] = createSignal(300);
  const [relevance, setRelevance] = createSignal(0.7);
  const [maxDaily, setMaxDaily] = createSignal(10);
  const [allowReactions, setAllowReactions] = createSignal(true);
  const [threadPolicy, setThreadPolicy] = createSignal("joined_only");
  const [quietStart, setQuietStart] = createSignal("");
  const [quietEnd, setQuietEnd] = createSignal("");

  // UI state
  const [saving, setSaving] = createSignal(false);
  const [message, setMessage] = createSignal<{ type: "success" | "error"; text: string } | null>(null);

  const flash = (type: "success" | "error", text: string) => {
    setMessage({ type, text });
    setTimeout(() => setMessage(null), 3000);
  };

  // Fetch known channels for the selector
  const channelsQuery = createQuery(() => ({
    queryKey: ["channels"],
    queryFn: () => client.request<{ channels: Channel[] }>(CHANNELS_QUERY),
  }));

  // Fetch organic config for the selected channel
  const configQuery = createQuery(() => ({
    queryKey: ["organicConfig", selectedChannel()],
    queryFn: () =>
      client.request<{ organicConfig: OrganicResponseConfig | null }>(ORGANIC_CONFIG_QUERY, {
        channelId: selectedChannel(),
      }),
    enabled: selectedChannel() !== "",
  }));

  /** Populate form fields from a loaded config. */
  function populateForm(cfg: OrganicResponseConfig) {
    setEnabled(cfg.enabled);
    setCooldown(cfg.cooldownSeconds);
    setRelevance(cfg.relevanceThreshold);
    setMaxDaily(cfg.maxDailyOrganic);
    setAllowReactions(cfg.allowReactions);
    setThreadPolicy(cfg.threadPolicy);
    setQuietStart(cfg.quietHoursStart ?? "");
    setQuietEnd(cfg.quietHoursEnd ?? "");
  }

  /** Load config for the entered channel ID. */
  function handleLoad() {
    const id = channelId().trim();
    if (!id) return;
    setSelectedChannel(id);
    // When the query resolves, populate the form
    void configQuery.refetch().then((result) => {
      const cfg = result.data?.organicConfig;
      if (cfg) {
        populateForm(cfg);
      } else {
        populateForm(defaultConfig(id));
      }
    });
  }

  /** Select a known channel from the list. */
  function handleSelectChannel(id: string) {
    setChannelId(id);
    setSelectedChannel(id);
    void configQuery.refetch().then((result) => {
      const cfg = result.data?.organicConfig;
      if (cfg) {
        populateForm(cfg);
      } else {
        populateForm(defaultConfig(id));
      }
    });
  }

  /** Save the current form state. */
  async function handleSave() {
    const id = selectedChannel();
    if (!id) return;

    setSaving(true);
    try {
      const input: Record<string, unknown> = {
        enabled: enabled(),
        cooldownSeconds: cooldown(),
        relevanceThreshold: relevance(),
        maxDailyOrganic: maxDaily(),
        allowReactions: allowReactions(),
        threadPolicy: threadPolicy(),
        quietHoursStart: quietStart() || null,
        quietHoursEnd: quietEnd() || null,
      };

      await client.request(UPDATE_ORGANIC_CONFIG_MUTATION, { channelId: id, input });
      void queryClient.invalidateQueries({ queryKey: ["organicConfig", id] });
      flash("success", "Configuration saved successfully");
    } catch (err) {
      flash("error", err instanceof Error ? err.message : "Failed to save configuration");
    } finally {
      setSaving(false);
    }
  }

  return (
    <AppShell activeTab="organic">
      <Show
        when={selectedChannel()}
        fallback={
          <div class="organic-view">
            <div class="organic-header">
              <h1>Organic Responses</h1>
            </div>

            {/* Channel selector */}
            <div class="organic-form__section">
              <h2 class="organic-form__section-title">Select Channel</h2>

              {/* Known channels list */}
              <Show when={channelsQuery.data?.channels?.length}>
                <div class="organic-form__row" style={{ "flex-wrap": "wrap", gap: "8px", "justify-content": "flex-start" }}>
                  <For each={channelsQuery.data!.channels}>
                    {(ch) => (
                      <button
                        type="button"
                        class="organic-channel-selector__load-btn"
                        onClick={() => handleSelectChannel(ch.id)}
                      >
                        {ch.name || ch.id}
                      </button>
                    )}
                  </For>
                </div>
              </Show>

              {/* Manual entry */}
              <div class="organic-channel-selector">
                <label for="organic-channel-id">Channel ID</label>
                <input
                  id="organic-channel-id"
                  type="text"
                  placeholder="Enter channel ID..."
                  value={channelId()}
                  onInput={(e) => setChannelId(e.currentTarget.value)}
                  onKeyDown={(e) => { if (e.key === "Enter") handleLoad(); }}
                />
                <button
                  type="button"
                  class="organic-channel-selector__load-btn"
                  disabled={!channelId().trim()}
                  onClick={handleLoad}
                >
                  Load Config
                </button>
              </div>
            </div>

            <div class="organic-empty">
              <span class="material-symbols-outlined organic-empty__icon">forum</span>
              <span class="organic-empty__text">Select a channel to configure organic responses</span>
            </div>
          </div>
        }
      >
        <div class="organic-view">
          <div class="organic-header">
            <div>
              <h1>Organic Responses</h1>
              <span style={{ "font-size": "var(--text-sm)", color: "var(--color-text-muted)" }}>
                Channel: {selectedChannel()}
              </span>
            </div>
            <div style={{ display: "flex", gap: "12px", "align-items": "center" }}>
              <span
                class="organic-status"
                classList={{
                  "organic-status--enabled": enabled(),
                  "organic-status--disabled": !enabled(),
                }}
              >
                {enabled() ? "Enabled" : "Disabled"}
              </span>
              <button
                type="button"
                class="organic-channel-selector__load-btn"
                onClick={() => { setSelectedChannel(""); setChannelId(""); }}
              >
                Change Channel
              </button>
            </div>
          </div>

          <Show when={configQuery.isLoading}>
            <div class="organic-loading">
              <span class="material-symbols-outlined organic-loading__icon">forum</span>
              <p class="organic-loading__title">Loading configuration...</p>
            </div>
          </Show>

          <Show when={!configQuery.isLoading}>
            <div class="organic-form">
              {/* General settings */}
              <div class="organic-form__section">
                <h2 class="organic-form__section-title">General</h2>

                <div class="organic-form__row">
                  <div class="organic-form__label">
                    <span class="organic-form__label-text">Enabled</span>
                    <span class="organic-form__label-hint">Allow the agent to send organic messages in this channel</span>
                  </div>
                  <div class="organic-form__control">
                    <label class="organic-toggle">
                      <input
                        type="checkbox"
                        checked={enabled()}
                        onChange={(e) => setEnabled(e.currentTarget.checked)}
                      />
                      <span class="organic-toggle__slider" />
                    </label>
                  </div>
                </div>

                <div class="organic-form__row">
                  <div class="organic-form__label">
                    <span class="organic-form__label-text">Cooldown (seconds)</span>
                    <span class="organic-form__label-hint">Minimum seconds between organic messages</span>
                  </div>
                  <div class="organic-form__control">
                    <input
                      type="number"
                      class="organic-form__input"
                      min="0"
                      step="1"
                      value={cooldown()}
                      onInput={(e) => setCooldown(parseInt(e.currentTarget.value, 10) || 0)}
                    />
                  </div>
                </div>

                <div class="organic-form__row">
                  <div class="organic-form__label">
                    <span class="organic-form__label-text">Max Daily Organic</span>
                    <span class="organic-form__label-hint">Maximum organic messages per day in this channel</span>
                  </div>
                  <div class="organic-form__control">
                    <input
                      type="number"
                      class="organic-form__input"
                      min="0"
                      step="1"
                      value={maxDaily()}
                      onInput={(e) => setMaxDaily(parseInt(e.currentTarget.value, 10) || 0)}
                    />
                  </div>
                </div>
              </div>

              {/* Behavior settings */}
              <div class="organic-form__section">
                <h2 class="organic-form__section-title">Behavior</h2>

                <div class="organic-form__row">
                  <div class="organic-form__label">
                    <span class="organic-form__label-text">Relevance Threshold</span>
                    <span class="organic-form__label-hint">Minimum relevance score (0-1) to trigger an organic response</span>
                  </div>
                  <div class="organic-form__control">
                    <div class="organic-form__range-wrap">
                      <input
                        type="range"
                        class="organic-form__range"
                        min="0"
                        max="1"
                        step="0.05"
                        value={relevance()}
                        onInput={(e) => setRelevance(parseFloat(e.currentTarget.value))}
                      />
                      <span class="organic-form__range-value">{relevance().toFixed(2)}</span>
                    </div>
                  </div>
                </div>

                <div class="organic-form__row">
                  <div class="organic-form__label">
                    <span class="organic-form__label-text">Allow Reactions</span>
                    <span class="organic-form__label-hint">Allow the agent to react to messages (emoji reactions)</span>
                  </div>
                  <div class="organic-form__control">
                    <label class="organic-toggle">
                      <input
                        type="checkbox"
                        checked={allowReactions()}
                        onChange={(e) => setAllowReactions(e.currentTarget.checked)}
                      />
                      <span class="organic-toggle__slider" />
                    </label>
                  </div>
                </div>

                <div class="organic-form__row">
                  <div class="organic-form__label">
                    <span class="organic-form__label-text">Thread Policy</span>
                    <span class="organic-form__label-hint">Controls which threads the agent may respond in organically</span>
                  </div>
                  <div class="organic-form__control">
                    <select
                      class="organic-form__select"
                      value={threadPolicy()}
                      onChange={(e) => setThreadPolicy(e.currentTarget.value)}
                    >
                      <option value="joined_only">Joined Only</option>
                      <option value="all">All</option>
                      <option value="none">None</option>
                    </select>
                  </div>
                </div>
              </div>

              {/* Quiet hours */}
              <div class="organic-form__section">
                <h2 class="organic-form__section-title">Quiet Hours</h2>

                <div class="organic-form__row">
                  <div class="organic-form__label">
                    <span class="organic-form__label-text">Start</span>
                    <span class="organic-form__label-hint">Time to begin suppressing organic messages (HH:MM)</span>
                  </div>
                  <div class="organic-form__control">
                    <input
                      type="time"
                      class="organic-form__input organic-form__input--time"
                      value={quietStart()}
                      onInput={(e) => setQuietStart(e.currentTarget.value)}
                    />
                  </div>
                </div>

                <div class="organic-form__row">
                  <div class="organic-form__label">
                    <span class="organic-form__label-text">End</span>
                    <span class="organic-form__label-hint">Time to resume organic messages (HH:MM)</span>
                  </div>
                  <div class="organic-form__control">
                    <input
                      type="time"
                      class="organic-form__input organic-form__input--time"
                      value={quietEnd()}
                      onInput={(e) => setQuietEnd(e.currentTarget.value)}
                    />
                  </div>
                </div>
              </div>

              {/* Actions */}
              <div class="organic-form__actions">
                <button
                  type="button"
                  class="organic-form__save-btn"
                  disabled={saving()}
                  onClick={handleSave}
                >
                  {saving() ? "Saving..." : "Save Configuration"}
                </button>

                <Show when={message()}>
                  <span
                    class="organic-message"
                    classList={{
                      "organic-message--success": message()!.type === "success",
                      "organic-message--error": message()!.type === "error",
                    }}
                  >
                    {message()!.text}
                  </span>
                </Show>
              </div>
            </div>
          </Show>
        </div>
      </Show>

      <footer class="app-shell__footer">MyPal</footer>
    </AppShell>
  );
};

export default OrganicResponseView;
