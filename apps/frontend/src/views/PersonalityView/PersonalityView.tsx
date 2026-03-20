// Copyright (c) MyPal contributors. See LICENSE for details.

import type { Component } from "solid-js";
import { createSignal, For, Show } from "solid-js";
import { createMutation, useQueryClient } from "@tanstack/solid-query";
import { usePersonalities } from "@mypal/ui/hooks";
import type { Personality, PersonalityInput } from "@mypal/ui/types";
import {
  CREATE_PERSONALITY_MUTATION,
  UPDATE_PERSONALITY_MUTATION,
  DELETE_PERSONALITY_MUTATION,
  SET_DEFAULT_PERSONALITY_MUTATION,
  PREVIEW_PERSONALITY_MUTATION,
} from "@mypal/ui/graphql/mutations";
import { client } from "../../graphql/client";
import AppShell from "../../components/AppShell";
import Modal from "../../components/Modal";
import "./PersonalityView.css";

/** Empty form state for creating a new personality. */
function emptyForm(): PersonalityInput {
  return {
    name: "",
    basePrompt: "",
    traits: [],
    tone: "",
    boundaries: [],
    quirks: [],
    adaptations: "",
    isDefault: false,
  };
}

const PersonalityView: Component = () => {
  const queryClient = useQueryClient();
  const personalities = usePersonalities(client);

  // Modal state
  const [modalOpen, setModalOpen] = createSignal(false);
  const [editingId, setEditingId] = createSignal<string | null>(null);
  const [form, setForm] = createSignal<PersonalityInput>(emptyForm());

  // Preview modal state
  const [previewOpen, setPreviewOpen] = createSignal(false);
  const [previewPersonalityId, setPreviewPersonalityId] = createSignal("");
  const [previewChannelType, setPreviewChannelType] = createSignal("discord");
  const [previewUserId, setPreviewUserId] = createSignal("new-user");
  const [previewTestMessage, setPreviewTestMessage] = createSignal("Hello, how are you?");
  const [previewResult, setPreviewResult] = createSignal("");

  // Message state
  const [message, setMessage] = createSignal<{ type: "success" | "error"; text: string } | null>(null);

  const clearMessage = () => setMessage(null);
  const flash = (type: "success" | "error", text: string) => {
    setMessage({ type, text });
    setTimeout(clearMessage, 3000);
  };

  // ── Mutations ──────────────────────────────────────────────────────────

  const createPersonality = createMutation(() => ({
    mutationFn: (input: PersonalityInput) =>
      client.request<{ createPersonality: { id: string } }>(CREATE_PERSONALITY_MUTATION, { input }),
    onSuccess: () => {
      flash("success", "Personality created");
      void queryClient.invalidateQueries({ queryKey: ["personalities"] });
      closeModal();
    },
    onError: (err: Error) => flash("error", err.message),
  }));

  const updatePersonality = createMutation(() => ({
    mutationFn: (vars: { id: string; input: PersonalityInput }) =>
      client.request<{ updatePersonality: { id: string } }>(UPDATE_PERSONALITY_MUTATION, vars),
    onSuccess: () => {
      flash("success", "Personality updated");
      void queryClient.invalidateQueries({ queryKey: ["personalities"] });
      closeModal();
    },
    onError: (err: Error) => flash("error", err.message),
  }));

  const deletePersonality = createMutation(() => ({
    mutationFn: (id: string) =>
      client.request<{ deletePersonality: boolean }>(DELETE_PERSONALITY_MUTATION, { id }),
    onSuccess: () => {
      flash("success", "Personality deleted");
      void queryClient.invalidateQueries({ queryKey: ["personalities"] });
    },
    onError: (err: Error) => flash("error", err.message),
  }));

  const setDefault = createMutation(() => ({
    mutationFn: (id: string) =>
      client.request<{ setDefaultPersonality: boolean }>(SET_DEFAULT_PERSONALITY_MUTATION, { id }),
    onSuccess: () => {
      flash("success", "Default personality updated");
      void queryClient.invalidateQueries({ queryKey: ["personalities"] });
    },
    onError: (err: Error) => flash("error", err.message),
  }));

  const previewPersonality = createMutation(() => ({
    mutationFn: (vars: { personalityId: string; userId: string; channelType: string; testMessage: string }) =>
      client.request<{ previewPersonality: string }>(PREVIEW_PERSONALITY_MUTATION, vars),
    onSuccess: (data) => {
      setPreviewResult(data.previewPersonality);
    },
    onError: (err: Error) => flash("error", err.message),
  }));

  // ── Helpers ────────────────────────────────────────────────────────────

  const openCreate = () => {
    setEditingId(null);
    setForm(emptyForm());
    setModalOpen(true);
  };

  const openEdit = (p: Personality) => {
    setEditingId(p.id);
    setForm({
      name: p.name,
      basePrompt: p.basePrompt,
      traits: [...p.traits],
      tone: p.tone,
      boundaries: [...p.boundaries],
      quirks: [...p.quirks],
      adaptations: p.adaptations ?? "",
      isDefault: p.isDefault,
    });
    setModalOpen(true);
  };

  const closeModal = () => {
    setModalOpen(false);
    setEditingId(null);
  };

  const openPreview = (p: Personality) => {
    setPreviewPersonalityId(p.id);
    setPreviewResult("");
    setPreviewOpen(true);
  };

  const closePreview = () => {
    setPreviewOpen(false);
    setPreviewResult("");
  };

  const handlePreview = () => {
    previewPersonality.mutate({
      personalityId: previewPersonalityId(),
      userId: previewUserId(),
      channelType: previewChannelType(),
      testMessage: previewTestMessage(),
    });
  };

  const handleSubmit = () => {
    const id = editingId();
    const input = form();
    if (!input.name.trim() || !input.basePrompt.trim()) {
      flash("error", "Name and base prompt are required");
      return;
    }
    if (id) {
      updatePersonality.mutate({ id, input });
    } else {
      createPersonality.mutate(input);
    }
  };

  /** Update a field on the form signal. */
  const updateField = <K extends keyof PersonalityInput>(key: K, value: PersonalityInput[K]) => {
    setForm((prev) => ({ ...prev, [key]: value }));
  };

  /** Parse a comma-separated string into a trimmed string array. */
  const parseTags = (raw: string): string[] =>
    raw
      .split(",")
      .map((s) => s.trim())
      .filter(Boolean);

  return (
    <AppShell activeTab="personalities">
      <div class="personality-view">
        <div class="personality-header">
          <h1>Personalities</h1>
          <div class="personality-header__actions">
            <Show when={message()}>
              <span
                class="personality-message"
                classList={{
                  "personality-message--success": message()?.type === "success",
                  "personality-message--error": message()?.type === "error",
                }}
              >
                {message()?.text}
              </span>
            </Show>
            <button class="save-btn" onClick={openCreate}>
              <span class="material-symbols-outlined" style={{ "font-size": "16px" }}>add</span>
              New Personality
            </button>
          </div>
        </div>

        {/* Personality cards */}
        <Show
          when={personalities.data && personalities.data.length > 0}
          fallback={
            <div class="personality-empty">
              <span class="material-symbols-outlined personality-empty__icon">psychology</span>
              <p class="personality-empty__text">No personalities configured yet</p>
            </div>
          }
        >
          <div class="personality-cards">
            <For each={personalities.data}>
              {(p) => (
                <div
                  class="personality-card"
                  classList={{ "personality-card--default": p.isDefault }}
                  onClick={() => openEdit(p)}
                >
                  <div class="personality-card__header">
                    <h3 class="personality-card__name">{p.name}</h3>
                    <div class="personality-card__badges">
                      <Show when={p.isDefault}>
                        <span class="personality-card__badge">Default</span>
                      </Show>
                    </div>
                  </div>
                  <span class="personality-card__tone">Tone: {p.tone || "—"}</span>
                  <Show when={p.traits.length > 0}>
                    <div class="personality-card__tags">
                      <For each={p.traits}>{(tag) => <span class="personality-card__tag">{tag}</span>}</For>
                    </div>
                  </Show>
                  <div class="personality-card__actions" onClick={(e) => e.stopPropagation()}>
                    <button class="personality-card__btn" onClick={() => openPreview(p)}>
                      <span class="material-symbols-outlined">visibility</span>
                      Preview
                    </button>
                    <button class="personality-card__btn" onClick={() => openEdit(p)}>
                      <span class="material-symbols-outlined">edit</span>
                      Edit
                    </button>
                    <Show when={!p.isDefault}>
                      <button
                        class="personality-card__btn"
                        onClick={() => setDefault.mutate(p.id)}
                      >
                        <span class="material-symbols-outlined">star</span>
                        Set Default
                      </button>
                      <button
                        class="personality-card__btn personality-card__btn--danger"
                        onClick={() => {
                          if (confirm(`Delete personality "${p.name}"?`)) {
                            deletePersonality.mutate(p.id);
                          }
                        }}
                      >
                        <span class="material-symbols-outlined">delete</span>
                        Delete
                      </button>
                    </Show>
                  </div>
                </div>
              )}
            </For>
          </div>
        </Show>
      </div>

      {/* Create / Edit Modal */}
      <Modal
        isOpen={modalOpen()}
        onClose={closeModal}
        title={editingId() ? "Edit Personality" : "New Personality"}
      >
        <div class="personality-form">
          {/* Name */}
          <div class="personality-form__field">
            <label class="personality-form__label">Name</label>
            <input
              class="personality-form__input"
              type="text"
              value={form().name}
              onInput={(e) => updateField("name", e.currentTarget.value)}
              placeholder="e.g. Friendly Assistant"
            />
          </div>

          {/* Base Prompt */}
          <div class="personality-form__field">
            <label class="personality-form__label">Base Prompt</label>
            <textarea
              class="personality-form__textarea"
              value={form().basePrompt}
              onInput={(e) => updateField("basePrompt", e.currentTarget.value)}
              placeholder="The core system prompt for this personality..."
            />
          </div>

          {/* Tone */}
          <div class="personality-form__field">
            <label class="personality-form__label">Tone</label>
            <input
              class="personality-form__input"
              type="text"
              value={form().tone ?? ""}
              onInput={(e) => updateField("tone", e.currentTarget.value)}
              placeholder="e.g. warm, professional, playful"
            />
          </div>

          {/* Traits */}
          <div class="personality-form__field">
            <label class="personality-form__label">Traits</label>
            <input
              class="personality-form__input"
              type="text"
              value={(form().traits ?? []).join(", ")}
              onInput={(e) => updateField("traits", parseTags(e.currentTarget.value))}
              placeholder="Comma-separated: curious, helpful, concise"
            />
            <span class="personality-form__hint">Comma-separated list</span>
          </div>

          {/* Boundaries */}
          <div class="personality-form__field">
            <label class="personality-form__label">Boundaries</label>
            <input
              class="personality-form__input"
              type="text"
              value={(form().boundaries ?? []).join(", ")}
              onInput={(e) => updateField("boundaries", parseTags(e.currentTarget.value))}
              placeholder="Comma-separated: no medical advice, no financial advice"
            />
            <span class="personality-form__hint">Comma-separated list</span>
          </div>

          {/* Quirks */}
          <div class="personality-form__field">
            <label class="personality-form__label">Quirks</label>
            <input
              class="personality-form__input"
              type="text"
              value={(form().quirks ?? []).join(", ")}
              onInput={(e) => updateField("quirks", parseTags(e.currentTarget.value))}
              placeholder="Comma-separated: uses analogies, references sci-fi"
            />
            <span class="personality-form__hint">Comma-separated list</span>
          </div>

          {/* Adaptations (JSON) */}
          <div class="personality-form__field">
            <label class="personality-form__label">Adaptations</label>
            <textarea
              class="personality-form__textarea"
              value={form().adaptations ?? ""}
              onInput={(e) => updateField("adaptations", e.currentTarget.value)}
              placeholder='JSON: {"discord": "Use shorter messages", "telegram": "Use markdown"}'
            />
            <span class="personality-form__hint">
              JSON map of channel name to adaptation text (optional)
            </span>
          </div>

          {/* Actions */}
          <div class="personality-form__actions">
            <button
              class="personality-form__btn personality-form__btn--secondary"
              onClick={closeModal}
            >
              Cancel
            </button>
            <button
              class="personality-form__btn personality-form__btn--primary"
              onClick={handleSubmit}
              disabled={createPersonality.isPending || updatePersonality.isPending}
            >
              {editingId() ? "Save Changes" : "Create"}
            </button>
          </div>
        </div>
      </Modal>

      {/* Preview Modal */}
      <Modal
        isOpen={previewOpen()}
        onClose={closePreview}
        title="Preview Personality"
      >
        <div class="personality-form">
          {/* Channel Type */}
          <div class="personality-form__field">
            <label class="personality-form__label">Channel Type</label>
            <select
              class="personality-form__input"
              value={previewChannelType()}
              onChange={(e) => setPreviewChannelType(e.currentTarget.value)}
            >
              <option value="discord">Discord</option>
              <option value="telegram">Telegram</option>
              <option value="email">Email</option>
              <option value="slack">Slack</option>
              <option value="sms">SMS</option>
            </select>
          </div>

          {/* User ID */}
          <div class="personality-form__field">
            <label class="personality-form__label">User ID</label>
            <input
              class="personality-form__input"
              type="text"
              value={previewUserId()}
              onInput={(e) => setPreviewUserId(e.currentTarget.value)}
              placeholder="new-user (low familiarity)"
            />
            <span class="personality-form__hint">
              Use "new-user" for low familiarity or a real user ID
            </span>
          </div>

          {/* Test Message */}
          <div class="personality-form__field">
            <label class="personality-form__label">Test Message</label>
            <textarea
              class="personality-form__textarea"
              value={previewTestMessage()}
              onInput={(e) => setPreviewTestMessage(e.currentTarget.value)}
              placeholder="Type a test message..."
            />
          </div>

          {/* Generate Preview */}
          <div class="personality-form__actions">
            <button
              class="personality-form__btn personality-form__btn--secondary"
              onClick={closePreview}
            >
              Close
            </button>
            <button
              class="personality-form__btn personality-form__btn--primary"
              onClick={handlePreview}
              disabled={previewPersonality.isPending}
            >
              {previewPersonality.isPending ? "Generating..." : "Generate Preview"}
            </button>
          </div>

          {/* Preview Output */}
          <Show when={previewResult()}>
            <div class="personality-form__field">
              <label class="personality-form__label">Assembled Prompt</label>
              <pre class="personality-preview__output">{previewResult()}</pre>
            </div>
          </Show>
        </div>
      </Modal>

      <footer class="app-shell__footer">MyPal</footer>
    </AppShell>
  );
};

export default PersonalityView;
