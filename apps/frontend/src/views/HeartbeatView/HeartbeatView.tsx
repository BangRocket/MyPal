// Copyright (c) MyPal contributors. See LICENSE for details.

import type { Component } from "solid-js";
import { createSignal, createMemo, For, Show } from "solid-js";
import { createMutation, createQuery, useQueryClient } from "@tanstack/solid-query";
import { useHeartbeatItems } from "@mypal/ui/hooks";
import type { HeartbeatItem, HeartbeatItemInput, HeartbeatLog } from "@mypal/ui/types";
import { HEARTBEAT_LOGS_QUERY } from "@mypal/ui/graphql/queries";
import {
  CREATE_HEARTBEAT_ITEM_MUTATION,
  UPDATE_HEARTBEAT_ITEM_MUTATION,
  DELETE_HEARTBEAT_ITEM_MUTATION,
  SNOOZE_HEARTBEAT_ITEM_MUTATION,
  COMPLETE_HEARTBEAT_ITEM_MUTATION,
} from "@mypal/ui/graphql/mutations";
import { client } from "../../graphql/client";
import AppShell from "../../components/AppShell";
import Modal from "../../components/Modal";
import "./HeartbeatView.css";

// ── Helpers ──────────────────────────────────────────────────────────────────

const PRIORITY_LABELS: Record<number, string> = {
  1: "Critical",
  2: "High",
  3: "Medium",
  4: "Low",
  5: "Minimal",
};

/** Format an ISO timestamp as a human-readable relative string. */
function relativeTime(iso: string | null): string {
  if (!iso) return "—";
  const date = new Date(iso);
  const now = Date.now();
  const diff = date.getTime() - now;
  const abs = Math.abs(diff);

  if (abs < 60_000) return diff > 0 ? "in a moment" : "just now";

  const minutes = Math.floor(abs / 60_000);
  const hours = Math.floor(abs / 3_600_000);
  const days = Math.floor(abs / 86_400_000);

  if (days > 0) {
    const label = days === 1 ? "1 day" : `${days} days`;
    return diff > 0 ? `in ${label}` : `${label} ago`;
  }
  if (hours > 0) {
    const label = hours === 1 ? "1 hour" : `${hours} hours`;
    return diff > 0 ? `in ${label}` : `${label} ago`;
  }
  const label = minutes === 1 ? "1 minute" : `${minutes} minutes`;
  return diff > 0 ? `in ${label}` : `${label} ago`;
}

/** Returns a short date+time string for log timestamps. */
function shortTimestamp(iso: string): string {
  const d = new Date(iso);
  return d.toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

/** Empty form state. */
function emptyForm(): HeartbeatItemInput {
  return {
    title: "",
    description: "",
    schedule: "0 */6 * * *",
    priority: 3,
    targetUser: "",
    targetChannel: "",
    context: "",
  };
}

type SortKey = "priority" | "status" | "nextRun";

// ── Component ────────────────────────────────────────────────────────────────

const HeartbeatView: Component = () => {
  const queryClient = useQueryClient();
  const heartbeatItems = useHeartbeatItems(client);

  // Modal state
  const [modalOpen, setModalOpen] = createSignal(false);
  const [editingId, setEditingId] = createSignal<string | null>(null);
  const [form, setForm] = createSignal<HeartbeatItemInput>(emptyForm());

  // Sort state
  const [sortKey, setSortKey] = createSignal<SortKey>("priority");
  const [sortAsc, setSortAsc] = createSignal(true);

  // Per-card expand/snooze state
  const [expandedLogs, setExpandedLogs] = createSignal<Set<string>>(new Set());
  const [snoozeTarget, setSnoozeTarget] = createSignal<string | null>(null);
  const [snoozeDate, setSnoozeDate] = createSignal("");

  // Message state
  const [message, setMessage] = createSignal<{ type: "success" | "error"; text: string } | null>(null);

  const clearMessage = () => setMessage(null);
  const flash = (type: "success" | "error", text: string) => {
    setMessage({ type, text });
    setTimeout(clearMessage, 3000);
  };

  // ── Sorted items ─────────────────────────────────────────────────────────

  const sortedItems = createMemo(() => {
    const items = heartbeatItems.data;
    if (!items) return [];
    const copy = [...items];
    const key = sortKey();
    const asc = sortAsc();
    copy.sort((a, b) => {
      let cmp = 0;
      if (key === "priority") {
        cmp = a.priority - b.priority;
      } else if (key === "status") {
        cmp = a.status.localeCompare(b.status);
      } else if (key === "nextRun") {
        const aTime = a.nextRun ? new Date(a.nextRun).getTime() : Infinity;
        const bTime = b.nextRun ? new Date(b.nextRun).getTime() : Infinity;
        cmp = aTime - bTime;
      }
      return asc ? cmp : -cmp;
    });
    return copy;
  });

  const handleSort = (key: SortKey) => {
    if (sortKey() === key) {
      setSortAsc((prev) => !prev);
    } else {
      setSortKey(key);
      setSortAsc(true);
    }
  };

  // ── Mutations ────────────────────────────────────────────────────────────

  const createItem = createMutation(() => ({
    mutationFn: (input: HeartbeatItemInput) =>
      client.request<{ createHeartbeatItem: { id: string } }>(CREATE_HEARTBEAT_ITEM_MUTATION, { input }),
    onSuccess: () => {
      flash("success", "Heartbeat item created");
      void queryClient.invalidateQueries({ queryKey: ["heartbeatItems"] });
      closeModal();
    },
    onError: (err: Error) => flash("error", err.message),
  }));

  const updateItem = createMutation(() => ({
    mutationFn: (vars: { id: string; input: HeartbeatItemInput }) =>
      client.request<{ updateHeartbeatItem: { id: string } }>(UPDATE_HEARTBEAT_ITEM_MUTATION, vars),
    onSuccess: () => {
      flash("success", "Heartbeat item updated");
      void queryClient.invalidateQueries({ queryKey: ["heartbeatItems"] });
      closeModal();
    },
    onError: (err: Error) => flash("error", err.message),
  }));

  const deleteItem = createMutation(() => ({
    mutationFn: (id: string) =>
      client.request<{ deleteHeartbeatItem: boolean }>(DELETE_HEARTBEAT_ITEM_MUTATION, { id }),
    onSuccess: () => {
      flash("success", "Heartbeat item deleted");
      void queryClient.invalidateQueries({ queryKey: ["heartbeatItems"] });
    },
    onError: (err: Error) => flash("error", err.message),
  }));

  const snoozeItem = createMutation(() => ({
    mutationFn: (vars: { id: string; until: string }) =>
      client.request<{ snoozeHeartbeatItem: { id: string } }>(SNOOZE_HEARTBEAT_ITEM_MUTATION, vars),
    onSuccess: () => {
      flash("success", "Heartbeat item snoozed");
      void queryClient.invalidateQueries({ queryKey: ["heartbeatItems"] });
      setSnoozeTarget(null);
      setSnoozeDate("");
    },
    onError: (err: Error) => flash("error", err.message),
  }));

  const completeItem = createMutation(() => ({
    mutationFn: (id: string) =>
      client.request<{ completeHeartbeatItem: boolean }>(COMPLETE_HEARTBEAT_ITEM_MUTATION, { id }),
    onSuccess: () => {
      flash("success", "Heartbeat item completed");
      void queryClient.invalidateQueries({ queryKey: ["heartbeatItems"] });
    },
    onError: (err: Error) => flash("error", err.message),
  }));

  // ── Modal helpers ────────────────────────────────────────────────────────

  const openCreate = () => {
    setEditingId(null);
    setForm(emptyForm());
    setModalOpen(true);
  };

  const openEdit = (item: HeartbeatItem) => {
    setEditingId(item.id);
    setForm({
      title: item.title,
      description: item.description,
      schedule: item.schedule,
      priority: item.priority,
      targetUser: item.targetUser,
      targetChannel: item.targetChannel,
      context: item.context,
    });
    setModalOpen(true);
  };

  const closeModal = () => {
    setModalOpen(false);
    setEditingId(null);
  };

  const handleSubmit = () => {
    const id = editingId();
    const input = form();
    if (!input.title.trim()) {
      flash("error", "Title is required");
      return;
    }
    if (id) {
      updateItem.mutate({ id, input });
    } else {
      createItem.mutate(input);
    }
  };

  const updateField = <K extends keyof HeartbeatItemInput>(key: K, value: HeartbeatItemInput[K]) => {
    setForm((prev) => ({ ...prev, [key]: value }));
  };

  // ── Log expand helpers ───────────────────────────────────────────────────

  const toggleLogs = (itemId: string) => {
    setExpandedLogs((prev) => {
      const next = new Set(prev);
      if (next.has(itemId)) {
        next.delete(itemId);
      } else {
        next.add(itemId);
      }
      return next;
    });
  };

  // ── Snooze helpers ───────────────────────────────────────────────────────

  const handleSnoozeSubmit = (id: string) => {
    const until = snoozeDate();
    if (!until) {
      flash("error", "Please select a snooze date/time");
      return;
    }
    snoozeItem.mutate({ id, until: new Date(until).toISOString() });
  };

  return (
    <AppShell activeTab="heartbeat">
      <div class="heartbeat-view">
        <div class="heartbeat-header">
          <h1>Heartbeat</h1>
          <div class="heartbeat-header__actions">
            <Show when={message()}>
              <span
                class="heartbeat-message"
                classList={{
                  "heartbeat-message--success": message()?.type === "success",
                  "heartbeat-message--error": message()?.type === "error",
                }}
              >
                {message()?.text}
              </span>
            </Show>
            <button class="save-btn" onClick={openCreate}>
              <span class="material-symbols-outlined" style={{ "font-size": "16px" }}>add</span>
              New Heartbeat
            </button>
          </div>
        </div>

        {/* Sort controls */}
        <div class="heartbeat-sort">
          <span class="heartbeat-sort__label">Sort:</span>
          <button
            class="heartbeat-sort__btn"
            classList={{ "heartbeat-sort__btn--active": sortKey() === "priority" }}
            onClick={() => handleSort("priority")}
          >
            Priority {sortKey() === "priority" ? (sortAsc() ? "\u2191" : "\u2193") : ""}
          </button>
          <button
            class="heartbeat-sort__btn"
            classList={{ "heartbeat-sort__btn--active": sortKey() === "status" }}
            onClick={() => handleSort("status")}
          >
            Status {sortKey() === "status" ? (sortAsc() ? "\u2191" : "\u2193") : ""}
          </button>
          <button
            class="heartbeat-sort__btn"
            classList={{ "heartbeat-sort__btn--active": sortKey() === "nextRun" }}
            onClick={() => handleSort("nextRun")}
          >
            Next Run {sortKey() === "nextRun" ? (sortAsc() ? "\u2191" : "\u2193") : ""}
          </button>
        </div>

        {/* Cards */}
        <Show
          when={sortedItems().length > 0}
          fallback={
            <div class="heartbeat-empty">
              <span class="material-symbols-outlined heartbeat-empty__icon">favorite</span>
              <p class="heartbeat-empty__text">No heartbeat items configured yet</p>
            </div>
          }
        >
          <div class="heartbeat-cards">
            <For each={sortedItems()}>
              {(item) => <HeartbeatCard
                item={item}
                expanded={expandedLogs().has(item.id)}
                snoozing={snoozeTarget() === item.id}
                snoozeDate={snoozeDate()}
                onEdit={() => openEdit(item)}
                onDelete={() => {
                  if (confirm(`Delete heartbeat "${item.title}"?`)) {
                    deleteItem.mutate(item.id);
                  }
                }}
                onComplete={() => completeItem.mutate(item.id)}
                onToggleLogs={() => toggleLogs(item.id)}
                onSnoozeStart={() => {
                  setSnoozeTarget(item.id);
                  setSnoozeDate("");
                }}
                onSnoozeCancel={() => setSnoozeTarget(null)}
                onSnoozeDate={setSnoozeDate}
                onSnoozeSubmit={() => handleSnoozeSubmit(item.id)}
              />}
            </For>
          </div>
        </Show>
      </div>

      {/* Create / Edit Modal */}
      <Modal
        isOpen={modalOpen()}
        onClose={closeModal}
        title={editingId() ? "Edit Heartbeat Item" : "New Heartbeat Item"}
      >
        <div class="heartbeat-form">
          {/* Title */}
          <div class="heartbeat-form__field">
            <label class="heartbeat-form__label">Title</label>
            <input
              class="heartbeat-form__input"
              type="text"
              value={form().title}
              onInput={(e) => updateField("title", e.currentTarget.value)}
              placeholder="e.g. Daily standup reminder"
            />
          </div>

          {/* Description */}
          <div class="heartbeat-form__field">
            <label class="heartbeat-form__label">Description</label>
            <textarea
              class="heartbeat-form__textarea"
              value={form().description}
              onInput={(e) => updateField("description", e.currentTarget.value)}
              placeholder="What should the agent check or remind about?"
            />
          </div>

          {/* Schedule */}
          <div class="heartbeat-form__field">
            <label class="heartbeat-form__label">Schedule</label>
            <input
              class="heartbeat-form__input"
              type="text"
              value={form().schedule}
              onInput={(e) => updateField("schedule", e.currentTarget.value)}
              placeholder="e.g. 0 */6 * * *"
            />
            <span class="heartbeat-form__hint">Cron expression for evaluation frequency</span>
          </div>

          {/* Priority */}
          <div class="heartbeat-form__field">
            <label class="heartbeat-form__label">Priority</label>
            <select
              class="heartbeat-form__select"
              value={form().priority}
              onChange={(e) => updateField("priority", parseInt(e.currentTarget.value, 10))}
            >
              <option value="1">1 - Critical</option>
              <option value="2">2 - High</option>
              <option value="3">3 - Medium</option>
              <option value="4">4 - Low</option>
              <option value="5">5 - Minimal</option>
            </select>
          </div>

          {/* Target User */}
          <div class="heartbeat-form__field">
            <label class="heartbeat-form__label">Target User</label>
            <input
              class="heartbeat-form__input"
              type="text"
              value={form().targetUser}
              onInput={(e) => updateField("targetUser", e.currentTarget.value)}
              placeholder="User to notify (channel user ID)"
            />
          </div>

          {/* Target Channel */}
          <div class="heartbeat-form__field">
            <label class="heartbeat-form__label">Target Channel</label>
            <input
              class="heartbeat-form__input"
              type="text"
              value={form().targetChannel}
              onInput={(e) => updateField("targetChannel", e.currentTarget.value)}
              placeholder="Channel to send through (e.g. telegram, discord)"
            />
          </div>

          {/* Context */}
          <div class="heartbeat-form__field">
            <label class="heartbeat-form__label">Context</label>
            <textarea
              class="heartbeat-form__textarea"
              value={form().context}
              onInput={(e) => updateField("context", e.currentTarget.value)}
              placeholder="Additional context for the agent when evaluating this item"
            />
          </div>

          {/* Actions */}
          <div class="heartbeat-form__actions">
            <button
              class="heartbeat-form__btn heartbeat-form__btn--secondary"
              onClick={closeModal}
            >
              Cancel
            </button>
            <button
              class="heartbeat-form__btn heartbeat-form__btn--primary"
              onClick={handleSubmit}
              disabled={createItem.isPending || updateItem.isPending}
            >
              {editingId() ? "Save Changes" : "Create"}
            </button>
          </div>
        </div>
      </Modal>

      <footer class="app-shell__footer">MyPal</footer>
    </AppShell>
  );
};

// ── HeartbeatCard sub-component ──────────────────────────────────────────────

interface HeartbeatCardProps {
  item: HeartbeatItem;
  expanded: boolean;
  snoozing: boolean;
  snoozeDate: string;
  onEdit: () => void;
  onDelete: () => void;
  onComplete: () => void;
  onToggleLogs: () => void;
  onSnoozeStart: () => void;
  onSnoozeCancel: () => void;
  onSnoozeDate: (v: string) => void;
  onSnoozeSubmit: () => void;
}

const HeartbeatCard: Component<HeartbeatCardProps> = (props) => {
  // Fetch logs when expanded
  const logs = createQuery<HeartbeatLog[]>(() => ({
    queryKey: ["heartbeatLogs", props.item.id],
    queryFn: async () => {
      const data = await client.request<{ heartbeatLogs: HeartbeatLog[] }>(HEARTBEAT_LOGS_QUERY, {
        itemId: props.item.id,
        limit: 20,
      });
      return data.heartbeatLogs;
    },
    enabled: props.expanded,
    refetchInterval: props.expanded ? 10_000 : false,
  }));

  return (
    <div class="heartbeat-card">
      <div class="heartbeat-card__header">
        <h3 class="heartbeat-card__title">{props.item.title}</h3>
        <div class="heartbeat-card__badges">
          <Show when={props.item.createdBy === "bot"}>
            <span class="heartbeat-badge heartbeat-badge--bot">
              <span class="material-symbols-outlined" style={{ "font-size": "12px" }}>smart_toy</span>
              Bot
            </span>
          </Show>
          <span class={`heartbeat-badge heartbeat-badge--priority-${props.item.priority}`}>
            P{props.item.priority}
          </span>
          <span class={`heartbeat-badge heartbeat-badge--${props.item.status}`}>
            {props.item.status}
          </span>
        </div>
      </div>

      <Show when={props.item.description}>
        <p class="heartbeat-card__description">{props.item.description}</p>
      </Show>

      <div class="heartbeat-card__meta">
        <span class="heartbeat-card__meta-item">
          <span class="material-symbols-outlined">schedule</span>
          {props.item.schedule}
        </span>
        <Show when={props.item.nextRun}>
          <span class="heartbeat-card__meta-item">
            <span class="material-symbols-outlined">update</span>
            Next: {relativeTime(props.item.nextRun)}
          </span>
        </Show>
        <Show when={props.item.lastRun}>
          <span class="heartbeat-card__meta-item">
            <span class="material-symbols-outlined">history</span>
            Last: {relativeTime(props.item.lastRun)}
          </span>
        </Show>
        <Show when={props.item.targetChannel}>
          <span class="heartbeat-card__meta-item">
            <span class="material-symbols-outlined">campaign</span>
            {props.item.targetChannel}
          </span>
        </Show>
      </div>

      {/* Actions */}
      <div class="heartbeat-card__actions">
        <button class="heartbeat-card__btn" onClick={props.onEdit}>
          <span class="material-symbols-outlined">edit</span>
          Edit
        </button>
        <Show when={props.item.status === "active"}>
          <button class="heartbeat-card__btn" onClick={props.onSnoozeStart}>
            <span class="material-symbols-outlined">snooze</span>
            Snooze
          </button>
          <button class="heartbeat-card__btn" onClick={props.onComplete}>
            <span class="material-symbols-outlined">check_circle</span>
            Complete
          </button>
        </Show>
        <button class="heartbeat-card__btn heartbeat-card__btn--danger" onClick={props.onDelete}>
          <span class="material-symbols-outlined">delete</span>
          Delete
        </button>
      </div>

      {/* Snooze input */}
      <Show when={props.snoozing}>
        <div class="heartbeat-snooze">
          <input
            class="heartbeat-snooze__input"
            type="datetime-local"
            value={props.snoozeDate}
            onInput={(e) => props.onSnoozeDate(e.currentTarget.value)}
          />
          <button class="heartbeat-snooze__confirm" onClick={props.onSnoozeSubmit} title="Confirm snooze">
            <span class="material-symbols-outlined" style={{ "font-size": "14px" }}>check</span>
          </button>
          <button class="heartbeat-snooze__cancel" onClick={props.onSnoozeCancel} title="Cancel">
            <span class="material-symbols-outlined" style={{ "font-size": "14px" }}>close</span>
          </button>
        </div>
      </Show>

      {/* Expandable logs */}
      <div class="heartbeat-logs">
        <button
          class="heartbeat-logs__toggle"
          classList={{ "heartbeat-logs__toggle--open": props.expanded }}
          onClick={props.onToggleLogs}
        >
          <span class="material-symbols-outlined">chevron_right</span>
          Execution Logs
        </button>
        <Show when={props.expanded}>
          <Show
            when={logs.data && logs.data.length > 0}
            fallback={<div class="heartbeat-logs__empty">No execution logs yet</div>}
          >
            <div class="heartbeat-logs__list">
              <For each={logs.data}>
                {(log) => (
                  <div class="heartbeat-logs__entry">
                    <div class="heartbeat-logs__entry-header">
                      <span class="heartbeat-logs__entry-action">{log.action}</span>
                      <span class="heartbeat-logs__entry-time">{shortTimestamp(log.timestamp)}</span>
                    </div>
                    <Show when={log.reason}>
                      <span class="heartbeat-logs__entry-reason">{log.reason}</span>
                    </Show>
                    <Show when={log.result}>
                      <span class="heartbeat-logs__entry-result">{log.result}</span>
                    </Show>
                  </div>
                )}
              </For>
            </div>
          </Show>
        </Show>
      </div>
    </div>
  );
};

export default HeartbeatView;
