// Copyright (c) MyPal contributors. See LICENSE for details.

import type { Component } from "solid-js";
import { createSignal, createMemo, For, Show } from "solid-js";
import { createMutation, createQuery, useQueryClient } from "@tanstack/solid-query";
import { SANDBOX_INSTANCES_QUERY } from "@mypal/ui/graphql/queries";
import {
  CREATE_SANDBOX_MUTATION,
  EXECUTE_SANDBOX_MUTATION,
  DESTROY_SANDBOX_MUTATION,
} from "@mypal/ui/graphql/mutations";
import { client } from "../../graphql/client";
import AppShell from "../../components/AppShell";
import Modal from "../../components/Modal";
import "./SandboxView.css";

// ── Types ────────────────────────────────────────────────────────────────────

interface SandboxInstance {
  id: string;
  image: string;
  status: string;
  userId: string;
  memLimit: number;
  cpuLimit: number;
  netPolicy: string;
  persistent: boolean;
  createdAt: string;
}

interface ExecResult {
  exitCode: number;
  stdout: string;
  stderr: string;
  durationMs: number;
}

// ── Helpers ──────────────────────────────────────────────────────────────────

function formatMemory(bytes: number): string {
  if (bytes <= 0) return "—";
  const mb = bytes / (1024 * 1024);
  if (mb >= 1024) return `${(mb / 1024).toFixed(1)} GB`;
  return `${Math.round(mb)} MB`;
}

function formatCpu(nanoCores: number): string {
  if (nanoCores <= 0) return "—";
  const cores = nanoCores / 1e9;
  if (cores >= 1) return `${cores.toFixed(1)} cores`;
  return `${(cores * 1000).toFixed(0)}m`;
}

function shortTimestamp(iso: string): string {
  const d = new Date(iso);
  return d.toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

function statusClass(status: string): string {
  switch (status.toLowerCase()) {
    case "running":
      return "sandbox-badge--running";
    case "stopped":
    case "exited":
      return "sandbox-badge--stopped";
    case "creating":
    case "starting":
      return "sandbox-badge--creating";
    default:
      return "sandbox-badge--stopped";
  }
}

// ── Component ────────────────────────────────────────────────────────────────

const SandboxView: Component = () => {
  const queryClient = useQueryClient();

  // Create modal state
  const [createOpen, setCreateOpen] = createSignal(false);
  const [createImage, setCreateImage] = createSignal("");
  const [createPersistent, setCreatePersistent] = createSignal(false);

  // Execute state
  const [execTarget, setExecTarget] = createSignal<string | null>(null);
  const [execCommand, setExecCommand] = createSignal("");
  const [execResult, setExecResult] = createSignal<ExecResult | null>(null);

  // Message state
  const [message, setMessage] = createSignal<{ type: "success" | "error"; text: string } | null>(null);

  const flash = (type: "success" | "error", text: string) => {
    setMessage({ type, text });
    setTimeout(() => setMessage(null), 3000);
  };

  // ── Query ──────────────────────────────────────────────────────────────────

  const instances = createQuery<SandboxInstance[]>(() => ({
    queryKey: ["sandboxInstances"],
    queryFn: async () => {
      const data = await client.request<{ sandboxInstances: SandboxInstance[] }>(SANDBOX_INSTANCES_QUERY);
      return data.sandboxInstances;
    },
    refetchInterval: 5000,
  }));

  // ── Mutations ──────────────────────────────────────────────────────────────

  const createSandbox = createMutation(() => ({
    mutationFn: (vars: { image: string; persistent: boolean }) =>
      client.request<{ createSandbox: { id: string; image: string; status: string } }>(
        CREATE_SANDBOX_MUTATION,
        vars,
      ),
    onSuccess: () => {
      flash("success", "Sandbox created");
      void queryClient.invalidateQueries({ queryKey: ["sandboxInstances"] });
      closeCreateModal();
    },
    onError: (err: Error) => flash("error", err.message),
  }));

  const executeSandbox = createMutation(() => ({
    mutationFn: (vars: { id: string; command: string }) =>
      client.request<{ executeSandbox: ExecResult }>(EXECUTE_SANDBOX_MUTATION, vars),
    onSuccess: (data) => {
      setExecResult(data.executeSandbox);
    },
    onError: (err: Error) => flash("error", err.message),
  }));

  const destroySandbox = createMutation(() => ({
    mutationFn: (id: string) =>
      client.request<{ destroySandbox: boolean }>(DESTROY_SANDBOX_MUTATION, { id }),
    onSuccess: () => {
      flash("success", "Sandbox destroyed");
      void queryClient.invalidateQueries({ queryKey: ["sandboxInstances"] });
      // Clear exec state if the destroyed sandbox was selected
      if (execTarget() !== null) {
        const found = instances.data?.find((i) => i.id === execTarget());
        if (!found) {
          setExecTarget(null);
          setExecResult(null);
        }
      }
    },
    onError: (err: Error) => flash("error", err.message),
  }));

  // ── Modal helpers ──────────────────────────────────────────────────────────

  const openCreateModal = () => {
    setCreateImage("");
    setCreatePersistent(false);
    setCreateOpen(true);
  };

  const closeCreateModal = () => {
    setCreateOpen(false);
  };

  const handleCreate = () => {
    const image = createImage().trim();
    if (!image) {
      flash("error", "Image name is required");
      return;
    }
    createSandbox.mutate({ image, persistent: createPersistent() });
  };

  // ── Exec helpers ───────────────────────────────────────────────────────────

  const openExec = (id: string) => {
    setExecTarget(id);
    setExecCommand("");
    setExecResult(null);
  };

  const handleExec = () => {
    const id = execTarget();
    const cmd = execCommand().trim();
    if (!id || !cmd) {
      flash("error", "Select a sandbox and enter a command");
      return;
    }
    setExecResult(null);
    executeSandbox.mutate({ id, command: cmd });
  };

  const instanceList = createMemo(() => instances.data ?? []);

  return (
    <AppShell activeTab="sandbox">
      <div class="sandbox-view">
        <div class="sandbox-header">
          <h1>Sandbox</h1>
          <div class="sandbox-header__actions">
            <Show when={message()}>
              <span
                class="sandbox-message"
                classList={{
                  "sandbox-message--success": message()?.type === "success",
                  "sandbox-message--error": message()?.type === "error",
                }}
              >
                {message()?.text}
              </span>
            </Show>
            <button class="save-btn" onClick={openCreateModal}>
              <span class="material-symbols-outlined" style={{ "font-size": "16px" }}>add</span>
              New Sandbox
            </button>
          </div>
        </div>

        {/* Instances table */}
        <Show
          when={instanceList().length > 0}
          fallback={
            <div class="sandbox-empty">
              <span class="material-symbols-outlined sandbox-empty__icon">terminal</span>
              <p class="sandbox-empty__text">No sandbox instances running</p>
            </div>
          }
        >
          <div class="sandbox-table-wrap">
            <table class="sandbox-table">
              <thead>
                <tr>
                  <th>ID</th>
                  <th>Image</th>
                  <th>Status</th>
                  <th>User</th>
                  <th>Memory</th>
                  <th>CPU</th>
                  <th>Net</th>
                  <th>Persistent</th>
                  <th>Created</th>
                  <th>Actions</th>
                </tr>
              </thead>
              <tbody>
                <For each={instanceList()}>
                  {(inst) => (
                    <tr>
                      <td class="sandbox-table__id" title={inst.id}>
                        {inst.id.length > 12 ? `${inst.id.slice(0, 12)}...` : inst.id}
                      </td>
                      <td>{inst.image}</td>
                      <td>
                        <span class={`sandbox-badge ${statusClass(inst.status)}`}>
                          {inst.status}
                        </span>
                      </td>
                      <td>{inst.userId || "—"}</td>
                      <td>{formatMemory(inst.memLimit)}</td>
                      <td>{formatCpu(inst.cpuLimit)}</td>
                      <td>{inst.netPolicy}</td>
                      <td>{inst.persistent ? "Yes" : "No"}</td>
                      <td>{shortTimestamp(inst.createdAt)}</td>
                      <td class="sandbox-table__actions">
                        <button
                          class="sandbox-table__btn"
                          onClick={() => openExec(inst.id)}
                          title="Execute command"
                        >
                          <span class="material-symbols-outlined">terminal</span>
                        </button>
                        <button
                          class="sandbox-table__btn sandbox-table__btn--danger"
                          onClick={() => {
                            if (confirm(`Destroy sandbox "${inst.id.slice(0, 12)}"?`)) {
                              destroySandbox.mutate(inst.id);
                            }
                          }}
                          title="Destroy sandbox"
                        >
                          <span class="material-symbols-outlined">delete</span>
                        </button>
                      </td>
                    </tr>
                  )}
                </For>
              </tbody>
            </table>
          </div>
        </Show>

        {/* Execute command panel */}
        <Show when={execTarget()}>
          <div class="sandbox-exec">
            <div class="sandbox-exec__header">
              <h2>Execute Command</h2>
              <span class="sandbox-exec__target">
                Sandbox: {execTarget()!.slice(0, 12)}...
              </span>
              <button
                class="sandbox-exec__close"
                onClick={() => { setExecTarget(null); setExecResult(null); }}
                title="Close"
              >
                <span class="material-symbols-outlined">close</span>
              </button>
            </div>
            <div class="sandbox-exec__input-row">
              <input
                class="sandbox-exec__input"
                type="text"
                value={execCommand()}
                onInput={(e) => setExecCommand(e.currentTarget.value)}
                onKeyDown={(e) => { if (e.key === "Enter") handleExec(); }}
                placeholder="Enter command..."
              />
              <button
                class="sandbox-exec__run"
                onClick={handleExec}
                disabled={executeSandbox.isPending}
              >
                {executeSandbox.isPending ? "Running..." : "Run"}
              </button>
            </div>
            <Show when={execResult()}>
              <div class="sandbox-exec__result">
                <div class="sandbox-exec__meta">
                  <span
                    class="sandbox-badge"
                    classList={{
                      "sandbox-badge--running": execResult()!.exitCode === 0,
                      "sandbox-badge--stopped": execResult()!.exitCode !== 0,
                    }}
                  >
                    Exit: {execResult()!.exitCode}
                  </span>
                  <span class="sandbox-exec__duration">
                    {execResult()!.durationMs}ms
                  </span>
                </div>
                <Show when={execResult()!.stdout}>
                  <div class="sandbox-exec__output">
                    <label>stdout</label>
                    <pre>{execResult()!.stdout}</pre>
                  </div>
                </Show>
                <Show when={execResult()!.stderr}>
                  <div class="sandbox-exec__output sandbox-exec__output--stderr">
                    <label>stderr</label>
                    <pre>{execResult()!.stderr}</pre>
                  </div>
                </Show>
              </div>
            </Show>
          </div>
        </Show>
      </div>

      {/* Create Sandbox Modal */}
      <Modal
        isOpen={createOpen()}
        onClose={closeCreateModal}
        title="New Sandbox"
      >
        <div class="sandbox-form">
          <div class="sandbox-form__field">
            <label class="sandbox-form__label">Image</label>
            <input
              class="sandbox-form__input"
              type="text"
              value={createImage()}
              onInput={(e) => setCreateImage(e.currentTarget.value)}
              placeholder="e.g. python:3.12-slim"
            />
          </div>
          <div class="sandbox-form__field sandbox-form__field--row">
            <label class="sandbox-form__label">Persistent</label>
            <input
              type="checkbox"
              checked={createPersistent()}
              onChange={(e) => setCreatePersistent(e.currentTarget.checked)}
            />
          </div>
          <div class="sandbox-form__actions">
            <button
              class="sandbox-form__btn sandbox-form__btn--secondary"
              onClick={closeCreateModal}
            >
              Cancel
            </button>
            <button
              class="sandbox-form__btn sandbox-form__btn--primary"
              onClick={handleCreate}
              disabled={createSandbox.isPending}
            >
              {createSandbox.isPending ? "Creating..." : "Create"}
            </button>
          </div>
        </div>
      </Modal>

      <footer class="app-shell__footer">MyPal</footer>
    </AppShell>
  );
};

export default SandboxView;
