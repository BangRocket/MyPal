// Copyright (c) MyPal contributors. See LICENSE for details.

import type { Component } from "solid-js";
import { createSignal, Show, For, onMount } from "solid-js";
import { A } from "@solidjs/router";
import { EMAIL_CONFIG_QUERY } from "@mypal/ui/graphql/queries";
import { GRAPHQL_ENDPOINT } from "../../graphql/client";
import { getStoredToken, setNeedsAuth } from "../../stores/authStore";
import AppShell from "../../components/AppShell";
import "./EmailSettingsView.css";

interface EmailFilterRule {
  field: string;
  pattern: string;
  action: string;
}

interface EmailConfig {
  enabled: boolean;
  imapHost: string;
  imapPort: number;
  imapUser: string;
  imapTls: boolean;
  smtpHost: string;
  smtpPort: number;
  smtpFrom: string;
  smtpTls: boolean;
  pollInterval: number;
  processedLabel: string;
  filters: EmailFilterRule[];
}

function graphqlHeaders(): Record<string, string> {
  const headers: Record<string, string> = { "Content-Type": "application/json" };
  const token = getStoredToken();
  if (token) headers["Authorization"] = `Bearer ${token}`;
  return headers;
}

const EmailSettingsView: Component = () => {
  const [config, setConfig] = createSignal<EmailConfig | null>(null);
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
        body: JSON.stringify({ query: EMAIL_CONFIG_QUERY }),
      });

      if (res.status === 401) {
        setNeedsAuth(true);
        return;
      }

      const data = await res.json();

      if (data.errors) {
        setError(data.errors[0]?.message ?? "Failed to load email configuration");
        return;
      }

      setConfig(data.data?.emailConfig ?? null);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to load email configuration");
    } finally {
      clearTimeout(timeoutId);
      setIsLoading(false);
    }
  });

  return (
    <AppShell activeTab="settings">
      <Show when={isLoading()}>
        <div class="email-settings-loading">
          <span class="material-symbols-outlined email-settings-loading__icon">mail</span>
          <p class="email-settings-loading__title">Loading email settings...</p>
        </div>
      </Show>

      <Show when={!isLoading()}>
        <div class="email-settings-view">
          <div class="email-settings-header">
            <div>
              <A href="/settings" class="email-settings-header__back">
                <span class="material-symbols-outlined">arrow_back</span>
                Settings
              </A>
              <h1>Email Channel</h1>
            </div>
            <Show when={config()}>
              <span
                class="email-settings-status"
                classList={{
                  "email-settings-status--enabled": config()!.enabled,
                  "email-settings-status--disabled": !config()!.enabled,
                }}
              >
                {config()!.enabled ? "Enabled" : "Disabled"}
              </span>
            </Show>
          </div>

          <Show when={error()}>
            <div class="email-settings-error">{error()}</div>
          </Show>

          <Show when={config()}>
            <section>
              <h2 class="email-settings-section__title">IMAP Settings</h2>
              <table class="email-settings-table">
                <thead>
                  <tr>
                    <th>Setting</th>
                    <th>Value</th>
                  </tr>
                </thead>
                <tbody>
                  <tr>
                    <td>Host</td>
                    <td><code class="email-settings-table__mono">{config()!.imapHost}</code></td>
                  </tr>
                  <tr>
                    <td>Port</td>
                    <td><code class="email-settings-table__mono">{config()!.imapPort}</code></td>
                  </tr>
                  <tr>
                    <td>User</td>
                    <td><code class="email-settings-table__mono">{config()!.imapUser}</code></td>
                  </tr>
                  <tr>
                    <td>TLS</td>
                    <td>
                      <span
                        class="email-settings-table__bool"
                        classList={{
                          "email-settings-table__bool--yes": config()!.imapTls,
                          "email-settings-table__bool--no": !config()!.imapTls,
                        }}
                      >
                        {config()!.imapTls ? "Yes" : "No"}
                      </span>
                    </td>
                  </tr>
                </tbody>
              </table>
            </section>

            <section>
              <h2 class="email-settings-section__title">SMTP Settings</h2>
              <table class="email-settings-table">
                <thead>
                  <tr>
                    <th>Setting</th>
                    <th>Value</th>
                  </tr>
                </thead>
                <tbody>
                  <tr>
                    <td>Host</td>
                    <td><code class="email-settings-table__mono">{config()!.smtpHost}</code></td>
                  </tr>
                  <tr>
                    <td>Port</td>
                    <td><code class="email-settings-table__mono">{config()!.smtpPort}</code></td>
                  </tr>
                  <tr>
                    <td>From Address</td>
                    <td><code class="email-settings-table__mono">{config()!.smtpFrom}</code></td>
                  </tr>
                  <tr>
                    <td>TLS</td>
                    <td>
                      <span
                        class="email-settings-table__bool"
                        classList={{
                          "email-settings-table__bool--yes": config()!.smtpTls,
                          "email-settings-table__bool--no": !config()!.smtpTls,
                        }}
                      >
                        {config()!.smtpTls ? "Yes" : "No"}
                      </span>
                    </td>
                  </tr>
                </tbody>
              </table>
            </section>

            <section>
              <h2 class="email-settings-section__title">General</h2>
              <table class="email-settings-table">
                <thead>
                  <tr>
                    <th>Setting</th>
                    <th>Value</th>
                  </tr>
                </thead>
                <tbody>
                  <tr>
                    <td>Poll Interval</td>
                    <td><code class="email-settings-table__mono">{config()!.pollInterval}s</code></td>
                  </tr>
                  <tr>
                    <td>Processed Label</td>
                    <td><code class="email-settings-table__mono">{config()!.processedLabel}</code></td>
                  </tr>
                </tbody>
              </table>
            </section>
          </Show>

          <Show when={config()?.filters && config()!.filters.length > 0}>
            <section>
              <h2 class="email-settings-section__title">Filter Rules</h2>
              <table class="email-settings-table">
                <thead>
                  <tr>
                    <th>Field</th>
                    <th>Pattern</th>
                    <th>Action</th>
                  </tr>
                </thead>
                <tbody>
                  <For each={config()!.filters}>
                    {(filter) => (
                      <tr>
                        <td><code class="email-settings-table__mono">{filter.field}</code></td>
                        <td><code class="email-settings-table__mono">{filter.pattern}</code></td>
                        <td>
                          <span
                            class="email-settings-table__bool"
                            classList={{
                              "email-settings-table__bool--yes": filter.action === "process",
                              "email-settings-table__bool--no": filter.action === "ignore",
                            }}
                          >
                            {filter.action}
                          </span>
                        </td>
                      </tr>
                    )}
                  </For>
                </tbody>
              </table>
            </section>
          </Show>

          <Show when={!config()?.filters || config()!.filters.length === 0}>
            <section>
              <h2 class="email-settings-section__title">Filter Rules</h2>
              <p class="email-settings-empty">No filter rules configured. All incoming emails will be processed.</p>
            </section>
          </Show>

          <div class="email-settings-note">
            <span class="material-symbols-outlined">info</span>
            <span>
              Email channel configuration is managed via <strong>mypal.yaml</strong> or environment variables.
            </span>
          </div>
        </div>
      </Show>

      <footer class="app-shell__footer">MyPal</footer>
    </AppShell>
  );
};

export default EmailSettingsView;
