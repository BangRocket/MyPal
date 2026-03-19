// Copyright (c) MyPal contributors. See LICENSE for details.

import { createQuery } from '@tanstack/solid-query';
import type { GraphQLClient } from 'graphql-request';
import { HEARTBEAT_ITEMS_QUERY, HEARTBEAT_LOGS_QUERY } from '../graphql/queries/index';
import type { HeartbeatItem, HeartbeatLog } from '../types/index';

interface HeartbeatItemsQueryResult {
  heartbeatItems: HeartbeatItem[];
}

interface HeartbeatLogsQueryResult {
  heartbeatLogs: HeartbeatLog[];
}

/**
 * Fetches all heartbeat items with a 10-second polling interval.
 */
export function useHeartbeatItems(client: GraphQLClient) {
  return createQuery<HeartbeatItem[]>(() => ({
    queryKey: ['heartbeatItems'],
    queryFn: async () => {
      const data = await client.request<HeartbeatItemsQueryResult>(HEARTBEAT_ITEMS_QUERY);
      return data.heartbeatItems;
    },
    refetchInterval: 10_000,
  }));
}

/**
 * Fetches execution logs for a specific heartbeat item.
 */
export function useHeartbeatLogs(client: GraphQLClient, itemId: () => string | null, limit?: number) {
  return createQuery<HeartbeatLog[]>(() => ({
    queryKey: ['heartbeatLogs', itemId()],
    queryFn: async () => {
      const id = itemId();
      if (!id) return [];
      const data = await client.request<HeartbeatLogsQueryResult>(HEARTBEAT_LOGS_QUERY, { itemId: id, limit });
      return data.heartbeatLogs;
    },
    enabled: !!itemId(),
    refetchInterval: 10_000,
  }));
}
