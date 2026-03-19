// Copyright (c) MyPal contributors. See LICENSE for details.

import { createQuery } from '@tanstack/solid-query';
import type { GraphQLClient } from 'graphql-request';
import { PERSONALITIES_QUERY } from '../graphql/queries/index';
import type { Personality } from '../types/index';

interface PersonalitiesQueryResult {
  personalities: Personality[];
}

/**
 * Fetches all personalities with a 10-second polling interval.
 *
 * @param client - GraphQL client instance
 * @returns solid-query result containing Personality[] or undefined while loading
 */
export function usePersonalities(client: GraphQLClient) {
  return createQuery<Personality[]>(() => ({
    queryKey: ['personalities'],
    queryFn: async () => {
      const data = await client.request<PersonalitiesQueryResult>(PERSONALITIES_QUERY);
      return data.personalities;
    },
    refetchInterval: 10_000,
  }));
}
