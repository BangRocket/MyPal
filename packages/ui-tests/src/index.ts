// Copyright (c) MyPal contributors. See LICENSE for details.

/**
 * @mypal/ui-tests — Mock implementations for testing
 *
 * This package provides mock implementations of the @mypal/ui hooks
 * for unit testing. It re-exports the same types and GraphQL queries/mutations
 * as the real package, but returns mock data instead of making network requests.
 *
 * Configured in vitest.config.ts:
 *   alias: { '@mypal/ui': uiTestsSrc }
 *
 * This allows tests to import from '@mypal/ui/hooks' and receive
 * mock implementations automatically.
 */

export * from "@mypal/ui/types";
export * from "@mypal/ui/graphql";
export * from "@mypal/ui/theme";
export * from "./hooks/index.js";
export * from "./graphql/mutations/index.js";
