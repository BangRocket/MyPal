// Copyright (c) MyPal contributors. See LICENSE for details.

/**
 * MemoryView — enhanced memory browser with three tabs:
 *
 * 1. Knowledge Graph — existing node list + detail + Cytoscape visualization.
 * 2. Vector Search  — semantic search with similarity scores, remember form,
 *                     and delete capability.
 * 3. Stats          — aggregate counts for vectors, entities, and relations.
 */

import type { Component } from 'solid-js';
import { For, Index, createMemo, createSignal, Show } from 'solid-js';
import { createMutation, useQueryClient } from '@tanstack/solid-query';
import { useMemory } from '@mypal/ui/hooks';
import type { MemoryNode, VectorSearchResult, GraphEntity, GraphRelation, MemoryStats } from '@mypal/ui/types';
import { UPDATE_MEMORY_NODE_MUTATION, DELETE_MEMORY_NODE_MUTATION, REMEMBER_MEMORY_MUTATION, FORGET_MEMORY_MUTATION } from '@mypal/ui/graphql/mutations';
import { VECTOR_SEARCH_QUERY, GRAPH_NEIGHBORS_QUERY, MEMORY_STATS_QUERY } from '@mypal/ui/graphql/queries';
import { client } from '../../graphql/client';
import AppShell from '../../components/AppShell';
import Modal from '../../components/Modal';
import { t } from '../../App';
import GraphVisualization from '../../components/GraphVisualization';
import './MemoryView.css';

type MemoryTab = 'graph' | 'vector' | 'stats';

/**
 * Groups an array of MemoryNode objects by their `type` field.
 */
function groupNodesByType(nodes: MemoryNode[]): Map<string, MemoryNode[]> {
  const groups = new Map<string, MemoryNode[]>();
  for (const node of nodes) {
    const key = node.type ?? '';
    const existing = groups.get(key);
    if (existing) {
      existing.push(node);
    } else {
      groups.set(key, [node]);
    }
  }
  return groups;
}

const MemoryView: Component = () => {
  const memory = useMemory(client);
  const queryClient = useQueryClient();

  // ── Tab state ──────────────────────────────────────────────────────────────
  const [activeTab, setActiveTab] = createSignal<MemoryTab>('graph');

  // ── Graph tab state ────────────────────────────────────────────────────────
  const [selectedNode, setSelectedNode] = createSignal<MemoryNode | null>(null);
  const [editModalOpen, setEditModalOpen] = createSignal(false);
  const [editLabel, setEditLabel] = createSignal('');
  const [editType, setEditType] = createSignal('');
  const [editProperties, setEditProperties] = createSignal<Array<{ key: string; value: string }>>([]);
  const [deleteModalOpen, setDeleteModalOpen] = createSignal(false);
  const [searchQuery, setSearchQuery] = createSignal('');

  // ── Vector search state ────────────────────────────────────────────────────
  const [vectorUserId, setVectorUserId] = createSignal('default');
  const [vectorQuery, setVectorQuery] = createSignal('');
  const [vectorResults, setVectorResults] = createSignal<VectorSearchResult[]>([]);
  const [vectorSearching, setVectorSearching] = createSignal(false);
  const [vectorSearchDone, setVectorSearchDone] = createSignal(false);

  // ── Remember form state ────────────────────────────────────────────────────
  const [rememberContent, setRememberContent] = createSignal('');
  const [rememberMetadata, setRememberMetadata] = createSignal('');
  const [rememberMessage, setRememberMessage] = createSignal<{ type: 'success' | 'error'; text: string } | null>(null);

  // ── Graph explorer state ───────────────────────────────────────────────────
  const [graphEntityId, setGraphEntityId] = createSignal('');
  const [graphDepth, setGraphDepth] = createSignal(1);
  const [graphEntities, setGraphEntities] = createSignal<GraphEntity[]>([]);
  const [graphRelations, setGraphRelations] = createSignal<GraphRelation[]>([]);
  const [graphExploring, setGraphExploring] = createSignal(false);
  const [graphExploreDone, setGraphExploreDone] = createSignal(false);

  // ── Stats state ────────────────────────────────────────────────────────────
  const [statsUserId, setStatsUserId] = createSignal('default');
  const [stats, setStats] = createSignal<MemoryStats | null>(null);
  const [statsLoading, setStatsLoading] = createSignal(false);

  // ── Graph tab mutations ────────────────────────────────────────────────────

  const updateNode = createMutation(() => ({
    mutationFn: (vars: { id: string; label: string; type: string; value: string; properties: string }) =>
      client.request(UPDATE_MEMORY_NODE_MUTATION, vars),
    onSuccess: (data: { updateMemoryNode: MemoryNode }) => {
      void queryClient.invalidateQueries({ queryKey: ['memory'] });
      const updated = data.updateMemoryNode;
      const propsMap: Record<string, string> = {};
      for (const { key, value } of editProperties()) {
        if (key.trim()) propsMap[key.trim()] = value;
      }
      setSelectedNode({ ...updated, properties: propsMap });
      setEditModalOpen(false);
    },
  }));

  const deleteNode = createMutation(() => ({
    mutationFn: (vars: { id: string }) => client.request(DELETE_MEMORY_NODE_MUTATION, vars),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['memory'] });
      setSelectedNode(null);
      setDeleteModalOpen(false);
    },
  }));

  // ── Vector tab mutations ───────────────────────────────────────────────────

  const rememberMemory = createMutation(() => ({
    mutationFn: (vars: { userId: string; content: string; metadata?: string }) =>
      client.request<{ rememberMemory: boolean }>(REMEMBER_MEMORY_MUTATION, vars),
    onSuccess: () => {
      setRememberContent('');
      setRememberMetadata('');
      setRememberMessage({ type: 'success', text: t('memory.rememberSuccess') });
      setTimeout(() => setRememberMessage(null), 3000);
    },
    onError: (err: Error) => {
      setRememberMessage({ type: 'error', text: err.message });
      setTimeout(() => setRememberMessage(null), 5000);
    },
  }));

  const forgetMemory = createMutation(() => ({
    mutationFn: (vars: { id: string }) =>
      client.request<{ forgetMemory: boolean }>(FORGET_MEMORY_MUTATION, vars),
    onSuccess: (_data: { forgetMemory: boolean }, vars: { id: string }) => {
      setVectorResults((prev) => prev.filter((r) => r.id !== vars.id));
      setRememberMessage({ type: 'success', text: t('memory.forgetSuccess') });
      setTimeout(() => setRememberMessage(null), 3000);
    },
    onError: (err: Error) => {
      setRememberMessage({ type: 'error', text: err.message });
      setTimeout(() => setRememberMessage(null), 5000);
    },
  }));

  // ── Graph tab helpers ──────────────────────────────────────────────────────

  const openEditModal = () => {
    const node = selectedNode();
    if (!node) return;
    setEditLabel(node.label);
    setEditType(node.type);
    const props = node.properties ? Object.entries(node.properties).map(([k, v]) => ({ key: k, value: v })) : [];
    setEditProperties(props);
    setEditModalOpen(true);
  };

  const addProperty = () =>
    setEditProperties((p) => [...p, { key: '', value: '' }]);

  const removeProperty = (index: number) =>
    setEditProperties((p) => p.filter((_, i) => i !== index));

  const updatePropertyKey = (index: number, key: string) =>
    setEditProperties((p) => p.map((item, i) => (i === index ? { ...item, key } : item)));

  const updatePropertyValue = (index: number, value: string) =>
    setEditProperties((p) => p.map((item, i) => (i === index ? { ...item, value } : item)));

  const handleEditSubmit = (e: Event) => {
    e.preventDefault();
    const node = selectedNode();
    if (!node) return;
    const propsMap: Record<string, string> = {};
    for (const { key, value } of editProperties()) {
      if (key.trim()) propsMap[key.trim()] = value;
    }
    updateNode.mutate({
      id: node.id,
      label: editLabel(),
      type: editType(),
      value: node.value ?? '',
      properties: JSON.stringify(propsMap),
    });
  };

  const handleDeleteConfirm = () => {
    const node = selectedNode();
    if (!node) return;
    deleteNode.mutate({ id: node.id });
  };

  const groupedNodes = createMemo(() => {
    const q = searchQuery().trim().toLowerCase();
    const nodes = memory.data?.nodes ?? [];
    const filtered = q
      ? nodes.filter(
          (n) =>
            (n.label ?? '').toLowerCase().includes(q) ||
            (n.type ?? '').toLowerCase().includes(q) ||
            (n.value ?? '').toLowerCase().includes(q)
        )
      : nodes;
    const map = groupNodesByType(filtered);
    return new Map(
      [...map.entries()]
        .sort(([a], [b]) => a.localeCompare(b))
        .map(([type, items]) => [
          type,
          [...items].sort((a, b) => (a.label ?? '').localeCompare(b.label ?? '')),
        ])
    );
  });

  const selectedNodeEdges = createMemo(() => {
    const node = selectedNode();
    if (!node || !memory.data?.edges) return { outgoing: [], incoming: [] };
    const outgoing = memory.data.edges.filter(e => e.sourceId === node.id);
    const incoming = memory.data.edges.filter(e => e.targetId === node.id);
    return { outgoing, incoming };
  });

  const findNodeById = (id: string): MemoryNode | undefined => {
    return memory.data?.nodes.find(n => n.id === id);
  };

  const hasNodes = () => (memory.data?.nodes?.length ?? 0) > 0;

  // ── Vector search handler ──────────────────────────────────────────────────

  const handleVectorSearch = async (e: Event) => {
    e.preventDefault();
    const q = vectorQuery().trim();
    const userId = vectorUserId().trim();
    if (!q || !userId) return;
    setVectorSearching(true);
    setVectorSearchDone(false);
    try {
      const data = await client.request<{ vectorSearch: VectorSearchResult[] }>(
        VECTOR_SEARCH_QUERY,
        { userId, query: q, topK: 20 }
      );
      setVectorResults(data.vectorSearch ?? []);
    } catch {
      setVectorResults([]);
    } finally {
      setVectorSearching(false);
      setVectorSearchDone(true);
    }
  };

  // ── Remember handler ───────────────────────────────────────────────────────

  const handleRemember = (e: Event) => {
    e.preventDefault();
    const content = rememberContent().trim();
    const userId = vectorUserId().trim();
    if (!content || !userId) return;
    const metadata = rememberMetadata().trim() || undefined;
    rememberMemory.mutate({ userId, content, metadata });
  };

  // ── Graph explorer handler ─────────────────────────────────────────────────

  const handleGraphExplore = async (e: Event) => {
    e.preventDefault();
    const entityId = graphEntityId().trim();
    if (!entityId) return;
    setGraphExploring(true);
    setGraphExploreDone(false);
    try {
      const data = await client.request<{ graphNeighbors: { entities: GraphEntity[]; relations: GraphRelation[] } }>(
        GRAPH_NEIGHBORS_QUERY,
        { entityId, depth: graphDepth() }
      );
      setGraphEntities(data.graphNeighbors?.entities ?? []);
      setGraphRelations(data.graphNeighbors?.relations ?? []);
    } catch {
      setGraphEntities([]);
      setGraphRelations([]);
    } finally {
      setGraphExploring(false);
      setGraphExploreDone(true);
    }
  };

  // ── Stats loader ───────────────────────────────────────────────────────────

  const loadStats = async () => {
    const userId = statsUserId().trim();
    if (!userId) return;
    setStatsLoading(true);
    try {
      const data = await client.request<{ memoryStats: MemoryStats }>(
        MEMORY_STATS_QUERY,
        { userId }
      );
      setStats(data.memoryStats ?? null);
    } catch {
      setStats(null);
    } finally {
      setStatsLoading(false);
    }
  };

  // ── Render ─────────────────────────────────────────────────────────────────

  return (
    <AppShell activeTab="memory" fullHeight>
      <div class="memory-view">
        {/* Tab bar */}
        <div class="memory-tabs">
          <button
            class="memory-tab"
            classList={{ 'memory-tab--active': activeTab() === 'graph' }}
            onClick={() => setActiveTab('graph')}
          >
            <span class="material-symbols-outlined memory-tab__icon">hub</span>
            {t('memory.tabGraph')}
          </button>
          <button
            class="memory-tab"
            classList={{ 'memory-tab--active': activeTab() === 'vector' }}
            onClick={() => setActiveTab('vector')}
          >
            <span class="material-symbols-outlined memory-tab__icon">search</span>
            {t('memory.tabVector')}
          </button>
          <button
            class="memory-tab"
            classList={{ 'memory-tab--active': activeTab() === 'stats' }}
            onClick={() => { setActiveTab('stats'); void loadStats(); }}
          >
            <span class="material-symbols-outlined memory-tab__icon">bar_chart</span>
            {t('memory.tabStats')}
          </button>
        </div>

        {/* ═══ Graph Tab ═══ */}
        <Show when={activeTab() === 'graph'}>
          <Show when={!memory.isLoading && !hasNodes()}>
            <div class="memory-empty">
              <span class="material-symbols-outlined memory-empty__icon">psychology</span>
              <p class="memory-empty__title">{t('memory.noMemory')}</p>
              <p class="memory-empty__hint">{t('memory.noMemoryHint')}</p>
            </div>
          </Show>

          <Show when={hasNodes()}>
            <div class="memory-container">
              {/* Sidebar */}
              <aside class="memory-sidebar">
                <div class="sidebar-header">
                  <h2>{t('memory.memoryIndex')}</h2>
                  <input
                    type="text"
                    class="search-box"
                    placeholder={t('memory.searchPlaceholder')}
                    value={searchQuery()}
                    onInput={(e) => setSearchQuery(e.currentTarget.value)}
                  />
                </div>

                <For each={[...groupedNodes().entries()]}>
                  {([type, nodes]) => (
                    <div class="memory-section">
                      <h3>{type.replace(/_/g, ' ').toUpperCase()}</h3>
                      <ul class="memory-list">
                        <For each={nodes}>
                          {(node) => (
                            <li class="memory-item" classList={{ 'memory-item--active': selectedNode()?.id === node.id }} onClick={() => setSelectedNode(node)}>
                              <div class="memory-item-avatar">
                                <span class="avatar-placeholder">{(node.label ?? '?').charAt(0)}</span>
                              </div>
                              <div class="memory-item-info">
                                <span class="memory-item-name">{node.label ?? ''}</span>
                                <span class="memory-item-role">{node.type ?? ''}</span>
                              </div>
                            </li>
                          )}
                        </For>
                      </ul>
                    </div>
                  )}
                </For>
              </aside>

              {/* Main Content */}
              <main class="memory-content">
                {selectedNode() ? (
                  <div class="person-detail">
                    <div class="person-header">
                      <div class="person-avatar">
                        <span class="avatar-large">{(selectedNode()!.label ?? '?').charAt(0)}</span>
                      </div>
                      <div class="person-info">
                        <h1>{selectedNode()!.label ?? ''}</h1>
                        <span class="person-role">{selectedNode()!.type ?? ''}</span>
                      </div>
                      <div class="person-actions">
                        <button class="action-btn" onClick={openEditModal} title={t('memory.editNode')}>
                          <span class="material-symbols-outlined">edit</span>
                        </button>
                        <button class="action-btn action-btn--danger" onClick={() => setDeleteModalOpen(true)} title={t('memory.deleteNode')}>
                          <span class="material-symbols-outlined">delete</span>
                        </button>
                      </div>
                    </div>

                    <Show when={selectedNode()!.properties && Object.keys(selectedNode()!.properties!).length > 0}>
                      <section class="detail-section">
                        <h2>{t('memory.properties')}</h2>
                        <div class="properties-list">
                          <For each={Object.entries(selectedNode()!.properties!)}>
                            {([key, value]) => (
                              <div class="property-item">
                                <span class="property-key">{key}:</span>
                                <span class="property-value">{value}</span>
                              </div>
                            )}
                          </For>
                        </div>
                      </section>
                    </Show>

                    <section class="detail-section">
                      <h2>{t('memory.graphViz')}</h2>
                      <GraphVisualization
                        nodes={memory.data?.nodes ?? []}
                        edges={memory.data?.edges ?? []}
                        selectedNodeId={selectedNode()?.id}
                        onNodeSelect={setSelectedNode}
                      />
                    </section>

                    <section class="detail-section">
                      <h2>{t('memory.connections')}</h2>
                      <Show when={selectedNodeEdges().outgoing.length > 0} fallback={<p class="no-connections">{t('memory.noOutgoing')}</p>}>
                        <div class="connections-list">
                          <h3>{t('memory.outgoing')} ({selectedNodeEdges().outgoing.length})</h3>
                          <ul class="edge-list">
                            <For each={selectedNodeEdges().outgoing}>
                              {(edge) => {
                                const targetNode = findNodeById(edge.targetId);
                                return (
                                  <li class="edge-item" onClick={() => targetNode && setSelectedNode(targetNode)}>
                                    <span class="edge-relation">{edge.relation ?? ''}</span>
                                    <span class="edge-arrow">{'\u2192'}</span>
                                    <span class="edge-target">{targetNode?.label ?? edge.targetId}</span>
                                    <span class="edge-type">({targetNode?.type ?? t('memory.unknown')})</span>
                                  </li>
                                );
                              }}
                            </For>
                          </ul>
                        </div>
                      </Show>

                      <Show when={selectedNodeEdges().incoming.length > 0}>
                        <div class="connections-list">
                          <h3>{t('memory.incoming')} ({selectedNodeEdges().incoming.length})</h3>
                          <ul class="edge-list">
                            <For each={selectedNodeEdges().incoming}>
                              {(edge) => {
                                const sourceNode = findNodeById(edge.sourceId);
                                return (
                                  <li class="edge-item" onClick={() => sourceNode && setSelectedNode(sourceNode)}>
                                    <span class="edge-source">{sourceNode?.label ?? edge.sourceId}</span>
                                    <span class="edge-type">({sourceNode?.type ?? t('memory.unknown')})</span>
                                    <span class="edge-arrow">{'\u2192'}</span>
                                    <span class="edge-relation">{edge.relation ?? ''}</span>
                                  </li>
                                );
                              }}
                            </For>
                          </ul>
                        </div>
                      </Show>
                    </section>
                  </div>
                ) : (
                  <div class="no-selection">
                    <span class="material-symbols-outlined">memory</span>
                    <p>{t('memory.selectToView')}</p>
                  </div>
                )}
              </main>
            </div>
          </Show>
        </Show>

        {/* ═══ Vector Search Tab ═══ */}
        <Show when={activeTab() === 'vector'}>
          <div class="vector-tab">
            <div class="vector-tab__panels">
              {/* Left panel: search + remember */}
              <div class="vector-tab__left">
                {/* Vector search form */}
                <section class="vector-section">
                  <h2 class="vector-section__title">
                    <span class="material-symbols-outlined">search</span>
                    {t('memory.vectorSearch')}
                  </h2>
                  <form class="vector-search-form" onSubmit={handleVectorSearch}>
                    <div class="vector-field">
                      <label class="vector-field__label">{t('memory.vectorUserId')}</label>
                      <input
                        type="text"
                        class="vector-field__input"
                        placeholder={t('memory.vectorUserIdPlaceholder')}
                        value={vectorUserId()}
                        onInput={(e) => setVectorUserId(e.currentTarget.value)}
                      />
                    </div>
                    <div class="vector-field">
                      <label class="vector-field__label">{t('memory.vectorSearch')}</label>
                      <input
                        type="text"
                        class="vector-field__input"
                        placeholder={t('memory.vectorSearchPlaceholder')}
                        value={vectorQuery()}
                        onInput={(e) => setVectorQuery(e.currentTarget.value)}
                      />
                    </div>
                    <button
                      type="submit"
                      class="modal-btn modal-btn--primary vector-search-btn"
                      disabled={vectorSearching() || !vectorQuery().trim() || !vectorUserId().trim()}
                    >
                      {vectorSearching() ? t('memory.vectorSearching') : t('memory.vectorSearchButton')}
                    </button>
                  </form>
                </section>

                {/* Remember form */}
                <section class="vector-section">
                  <h2 class="vector-section__title">
                    <span class="material-symbols-outlined">add_circle</span>
                    {t('memory.rememberTitle')}
                  </h2>
                  <Show when={rememberMessage()}>
                    <div
                      class="vector-message"
                      classList={{
                        'vector-message--success': rememberMessage()?.type === 'success',
                        'vector-message--error': rememberMessage()?.type === 'error',
                      }}
                    >
                      {rememberMessage()?.text}
                    </div>
                  </Show>
                  <form class="vector-search-form" onSubmit={handleRemember}>
                    <div class="vector-field">
                      <textarea
                        class="vector-field__textarea"
                        placeholder={t('memory.rememberPlaceholder')}
                        value={rememberContent()}
                        onInput={(e) => setRememberContent(e.currentTarget.value)}
                        rows={3}
                      />
                    </div>
                    <div class="vector-field">
                      <input
                        type="text"
                        class="vector-field__input"
                        placeholder={t('memory.rememberMetadataPlaceholder')}
                        value={rememberMetadata()}
                        onInput={(e) => setRememberMetadata(e.currentTarget.value)}
                      />
                    </div>
                    <button
                      type="submit"
                      class="modal-btn modal-btn--primary vector-search-btn"
                      disabled={rememberMemory.isPending || !rememberContent().trim()}
                    >
                      {rememberMemory.isPending ? t('memory.remembering') : t('memory.rememberButton')}
                    </button>
                  </form>
                </section>

                {/* Graph explorer */}
                <section class="vector-section">
                  <h2 class="vector-section__title">
                    <span class="material-symbols-outlined">account_tree</span>
                    {t('memory.graphExplorer')}
                  </h2>
                  <form class="vector-search-form" onSubmit={handleGraphExplore}>
                    <div class="vector-field">
                      <label class="vector-field__label">{t('memory.graphEntityId')}</label>
                      <input
                        type="text"
                        class="vector-field__input"
                        placeholder={t('memory.graphEntityIdPlaceholder')}
                        value={graphEntityId()}
                        onInput={(e) => setGraphEntityId(e.currentTarget.value)}
                      />
                    </div>
                    <div class="vector-field">
                      <label class="vector-field__label">{t('memory.graphDepth')}</label>
                      <input
                        type="number"
                        class="vector-field__input"
                        min={1}
                        max={5}
                        value={graphDepth()}
                        onInput={(e) => setGraphDepth(parseInt(e.currentTarget.value, 10) || 1)}
                      />
                    </div>
                    <button
                      type="submit"
                      class="modal-btn modal-btn--primary vector-search-btn"
                      disabled={graphExploring() || !graphEntityId().trim()}
                    >
                      {graphExploring() ? t('memory.graphExploring') : t('memory.graphExploreButton')}
                    </button>
                  </form>
                </section>
              </div>

              {/* Right panel: results */}
              <div class="vector-tab__right">
                {/* Vector search results */}
                <Show when={vectorSearchDone()}>
                  <section class="vector-section">
                    <h2 class="vector-section__title">
                      <span class="material-symbols-outlined">list</span>
                      {t('memory.vectorSearch')} ({vectorResults().length})
                    </h2>
                    <Show
                      when={vectorResults().length > 0}
                      fallback={<p class="vector-empty">{t('memory.vectorNoResults')}</p>}
                    >
                      <ul class="vector-results">
                        <For each={vectorResults()}>
                          {(result) => (
                            <li class="vector-result">
                              <div class="vector-result__header">
                                <span class="vector-result__score">
                                  {t('memory.vectorScore')}: {Math.round(result.score * 100)}%
                                </span>
                                <span class="vector-result__time">
                                  {result.createdAt ? new Date(result.createdAt).toLocaleString() : ''}
                                </span>
                                <button
                                  class="action-btn action-btn--danger vector-result__delete"
                                  onClick={() => {
                                    if (confirm(t('memory.vectorDeleteConfirm'))) {
                                      forgetMemory.mutate({ id: result.id });
                                    }
                                  }}
                                  title={t('memory.deleteNode')}
                                >
                                  <span class="material-symbols-outlined">delete</span>
                                </button>
                              </div>
                              <p class="vector-result__content">{result.content}</p>
                              <Show when={result.metadata}>
                                <span class="vector-result__metadata">
                                  {t('memory.vectorMetadata')}: {result.metadata}
                                </span>
                              </Show>
                            </li>
                          )}
                        </For>
                      </ul>
                    </Show>
                  </section>
                </Show>

                {/* Graph explorer results */}
                <Show when={graphExploreDone()}>
                  <section class="vector-section">
                    <h2 class="vector-section__title">
                      <span class="material-symbols-outlined">account_tree</span>
                      {t('memory.graphExplorer')}
                    </h2>
                    <Show
                      when={graphEntities().length > 0 || graphRelations().length > 0}
                      fallback={<p class="vector-empty">{t('memory.graphNoResults')}</p>}
                    >
                      <Show when={graphEntities().length > 0}>
                        <h3 class="graph-explorer__subheading">{t('memory.graphEntities')} ({graphEntities().length})</h3>
                        <ul class="graph-explorer__list">
                          <For each={graphEntities()}>
                            {(entity) => (
                              <li class="graph-explorer__item">
                                <span class="graph-explorer__name">{entity.name}</span>
                                <span class="graph-explorer__type">{entity.type}</span>
                                <button
                                  class="graph-explorer__explore-btn"
                                  onClick={() => {
                                    setGraphEntityId(entity.id);
                                    void handleGraphExplore(new Event('submit'));
                                  }}
                                  title={t('memory.graphExploreButton')}
                                >
                                  <span class="material-symbols-outlined">arrow_forward</span>
                                </button>
                              </li>
                            )}
                          </For>
                        </ul>
                      </Show>
                      <Show when={graphRelations().length > 0}>
                        <h3 class="graph-explorer__subheading">{t('memory.graphRelations')} ({graphRelations().length})</h3>
                        <ul class="graph-explorer__list">
                          <For each={graphRelations()}>
                            {(rel) => (
                              <li class="graph-explorer__item">
                                <span class="graph-explorer__from">{rel.fromId}</span>
                                <span class="graph-explorer__rel-type">{rel.type}</span>
                                <span class="graph-explorer__arrow">{'\u2192'}</span>
                                <span class="graph-explorer__to">{rel.toId}</span>
                                <Show when={rel.weight > 0}>
                                  <span class="graph-explorer__weight">({rel.weight.toFixed(2)})</span>
                                </Show>
                              </li>
                            )}
                          </For>
                        </ul>
                      </Show>
                    </Show>
                  </section>
                </Show>

                {/* Empty state when nothing searched yet */}
                <Show when={!vectorSearchDone() && !graphExploreDone()}>
                  <div class="no-selection">
                    <span class="material-symbols-outlined">search</span>
                    <p>{t('memory.vectorSearch')}</p>
                  </div>
                </Show>
              </div>
            </div>
          </div>
        </Show>

        {/* ═══ Stats Tab ═══ */}
        <Show when={activeTab() === 'stats'}>
          <div class="stats-tab">
            <div class="stats-tab__header">
              <h2 class="stats-tab__title">{t('memory.statsTitle')}</h2>
              <div class="stats-tab__user-row">
                <label class="vector-field__label">{t('memory.vectorUserId')}</label>
                <input
                  type="text"
                  class="vector-field__input stats-tab__user-input"
                  value={statsUserId()}
                  onInput={(e) => setStatsUserId(e.currentTarget.value)}
                  placeholder={t('memory.vectorUserIdPlaceholder')}
                />
                <button
                  class="modal-btn modal-btn--primary"
                  onClick={() => void loadStats()}
                  disabled={statsLoading() || !statsUserId().trim()}
                >
                  {statsLoading() ? t('memory.statsLoading') : t('memory.vectorSearchButton')}
                </button>
              </div>
            </div>
            <Show
              when={stats()}
              fallback={
                <Show when={statsLoading()}>
                  <p class="stats-tab__loading">{t('memory.statsLoading')}</p>
                </Show>
              }
            >
              <div class="stats-cards">
                <div class="stats-card">
                  <span class="material-symbols-outlined stats-card__icon">database</span>
                  <span class="stats-card__value">{stats()!.vectorCount}</span>
                  <span class="stats-card__label">{t('memory.statsVectorCount')}</span>
                </div>
                <div class="stats-card">
                  <span class="material-symbols-outlined stats-card__icon">hub</span>
                  <span class="stats-card__value">{stats()!.entityCount}</span>
                  <span class="stats-card__label">{t('memory.statsEntityCount')}</span>
                </div>
                <div class="stats-card">
                  <span class="material-symbols-outlined stats-card__icon">link</span>
                  <span class="stats-card__value">{stats()!.relationCount}</span>
                  <span class="stats-card__label">{t('memory.statsRelationCount')}</span>
                </div>
              </div>
            </Show>
          </div>
        </Show>
      </div>

      {/* Edit Modal */}
      <Modal isOpen={editModalOpen()} onClose={() => setEditModalOpen(false)} title={t('memory.editNode')}>
        <form class="memory-modal-form" onSubmit={handleEditSubmit}>
          <div class="memory-modal-field">
            <label for="edit-label">{t('memory.label')}</label>
            <input
              id="edit-label"
              type="text"
              value={editLabel()}
              onInput={(e) => setEditLabel(e.currentTarget.value)}
              required
            />
          </div>
          <div class="memory-modal-field">
            <label for="edit-type">{t('memory.type')}</label>
            <input
              id="edit-type"
              type="text"
              value={editType()}
              onInput={(e) => setEditType(e.currentTarget.value)}
              required
            />
          </div>

          <div class="memory-modal-properties">
            <div class="memory-modal-properties-header">
              <span class="memory-modal-properties-label">{t('memory.properties')}</span>
              <button type="button" class="memory-modal-add-prop" onClick={addProperty}>
                <span class="material-symbols-outlined">add</span>
                {t('memory.addProperty')}
              </button>
            </div>
            <Show when={editProperties().length > 0}>
              <div class="memory-modal-props-list">
                <Index each={editProperties()}>
                  {(prop, i) => (
                    <div class="memory-modal-prop-row">
                      <input
                        type="text"
                        placeholder={t('memory.key')}
                        value={prop().key}
                        onInput={(e) => updatePropertyKey(i, e.currentTarget.value)}
                      />
                      <span class="prop-row-sep">:</span>
                      <input
                        type="text"
                        placeholder={t('memory.value')}
                        value={prop().value}
                        onInput={(e) => updatePropertyValue(i, e.currentTarget.value)}
                      />
                      <button
                        type="button"
                        class="prop-row-remove"
                        onClick={() => removeProperty(i)}
                        title={t('memory.remove')}
                      >
                        <span class="material-symbols-outlined">close</span>
                      </button>
                    </div>
                  )}
                </Index>
              </div>
            </Show>
            <Show when={editProperties().length === 0}>
              <p class="memory-modal-props-empty">{t('memory.noProperties')}</p>
            </Show>
          </div>

          <div class="memory-modal-actions">
            <button type="button" class="modal-btn modal-btn--secondary" onClick={() => setEditModalOpen(false)}>
              {t('common.cancel')}
            </button>
            <button type="submit" class="modal-btn modal-btn--primary" disabled={updateNode.isPending}>
              {updateNode.isPending ? t('memory.saving') : t('common.save')}
            </button>
          </div>
        </form>
      </Modal>

      {/* Delete Confirmation Modal */}
      <Modal isOpen={deleteModalOpen()} onClose={() => setDeleteModalOpen(false)} title={t('memory.deleteNode')}>
        <div class="memory-modal-confirm">
          <p>{t('memory.deleteConfirm', { name: selectedNode()?.label || t('memory.unknown') })}</p>
          <p class="memory-modal-warning">{t('memory.deleteWarning')}</p>
          <div class="memory-modal-actions">
            <button type="button" class="modal-btn modal-btn--secondary" onClick={() => setDeleteModalOpen(false)}>
              {t('common.cancel')}
            </button>
            <button
              type="button"
              class="modal-btn modal-btn--danger"
              onClick={handleDeleteConfirm}
              disabled={deleteNode.isPending}
            >
              {deleteNode.isPending ? t('memory.deleting') : t('common.delete')}
            </button>
          </div>
        </div>
      </Modal>
    </AppShell>
  );
};

export default MemoryView;
