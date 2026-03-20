// Copyright (c) MyPal contributors. See LICENSE for details.

/**
 * JSON Schema for MyPal configuration
 * Defines all configuration fields with types, validation, and dependencies
 */
export interface ConfigSchema {
  $schema: string;
  type: string;
  properties: Record<string, SchemaProperty>;
  required?: string[];
}

/** Condition shape used in JSON Schema dependency rules. */
export interface SchemaConditionNode {
  const?: unknown;
  properties?: Record<string, SchemaConditionNode>;
}

export interface SchemaCondition {
  properties?: Record<string, SchemaConditionNode>;
  oneOf?: SchemaCondition[];
}

export interface SchemaProperty {
  type: string;
  title: string;
  description: string;
  default?: unknown;
  properties?: Record<string, SchemaProperty>;
  enum?: string[];
  minLength?: number;
  maxLength?: number;
  minimum?: number;
  maximum?: number;
  pattern?: string;
  format?: string;
  dependencies?: Record<string, SchemaCondition>;
  oneOf?: SchemaCondition[];
  allOf?: SchemaCondition[];
  if?: SchemaCondition;
  then?: SchemaCondition;
  else?: SchemaCondition;
}

/**
 * Configuration schema with conditional rendering and validation rules
 */
export const configSchema: ConfigSchema = {
  $schema: "http://json-schema.org/draft-07/schema#",
  type: "object",
  properties: {
    // ========== GENERAL CONFIGURATION ==========
    agentName: {
      type: "string",
      title: "Agent Name",
      description: "Display name for this agent instance",
      default: "agent-01",
      minLength: 1,
      maxLength: 50,
    },
    provider: {
      type: "string",
      title: "AI Provider",
      description: "AI provider used for agent tasks",
      enum: ["openai", "openrouter", "ollama", "anthropic", "docker-model-runner", "opencode-zen", "openai-compatible"],
      default: "ollama",
    },
    model: {
      type: "string",
      title: "Default Model",
      description: "Choose the default model for agent tasks",
      default: "llama3.2:latest",
    },

    // Provider-specific fields with dependencies
    apiKey: {
      type: "string",
      title: "API Key",
      description: "API key for the selected provider",
      format: "password",
      dependencies: {
        provider: {
          oneOf: [
            { properties: { provider: { const: "openai" } } },
            { properties: { provider: { const: "openrouter" } } },
            { properties: { provider: { const: "opencode-zen" } } },
            { properties: { provider: { const: "openai-compatible" } } },
          ],
        },
      },
    },
    baseURL: {
      type: "string",
      title: "Base URL",
      description: "Base URL for the OpenAI-compatible provider API (e.g. http://localhost:8000/v1)",
      dependencies: {
        provider: {
          properties: { provider: { const: "openai-compatible" } },
        },
      },
    },
    ollamaHost: {
      type: "string",
      title: "Ollama Host",
      description: "Ollama server host address",
      default: "http://localhost:11434",
      dependencies: {
        provider: {
          properties: { provider: { const: "ollama" } },
        },
      },
    },
    ollamaApiKey: {
      type: "string",
      title: "Ollama API Key",
      description: "Optional API key for protected or remote Ollama instances",
      format: "password",
      dependencies: {
        provider: {
          properties: { provider: { const: "ollama" } },
        },
      },
    },
    anthropicApiKey: {
      type: "string",
      title: "Anthropic API Key",
      description: "API key from console.anthropic.com",
      format: "password",
      dependencies: {
        provider: {
          properties: { provider: { const: "anthropic" } },
        },
      },
    },
    dockerModelRunnerEndpoint: {
      type: "string",
      title: "Docker Model Runner Endpoint",
      description: "Docker Model Runner API endpoint (default: http://localhost:12434/engines/v1)",
      default: "http://localhost:12434/engines/v1",
      dependencies: {
        provider: {
          properties: { provider: { const: "docker-model-runner" } },
        },
      },
    },

    // ========== AGENT CAPABILITIES ==========
    capabilities: {
      type: "object",
      title: "Agent Capabilities",
      description: "Enable or disable core agent features such as browser automation, terminal commands, subagents, memory, MCP integration, filesystem access, and session interaction.",
      properties: {
        browser: {
          type: "boolean",
          title: "Browser",
          description: "Enable browser automation capabilities",
          default: false,
        },
        terminal: {
          type: "boolean",
          title: "Terminal",
          description: "Enable terminal command execution",
          default: false,
        },
        subagents: {
          type: "boolean",
          title: "Subagents",
          description: "Enable spawning of subagents",
          default: true,
        },
        memory: {
          type: "boolean",
          title: "Memory",
          description: "Enable long-term memory storage",
          default: true,
        },
        mcp: {
          type: "boolean",
          title: "MCP",
          description: "Enable Model Context Protocol servers",
          default: true,
        },
        filesystem: {
          type: "boolean",
          title: "Filesystem",
          description: "Enable direct file read and write access on the server",
          default: true,
        },
        sessions: {
          type: "boolean",
          title: "Session Interaction",
          description: "Enable interaction with and inspection of other active agent sessions",
          default: true,
        },
      },
    },

    // ========== DATABASE CONFIGURATION ==========
    databaseDriver: {
      type: "string",
      title: "Database Driver",
      description: "Database driver to use",
      enum: ["sqlite", "postgres", "mysql"],
      default: "sqlite",
    },
    databaseDSN: {
      type: "string",
      title: "Database DSN",
      description: "Database connection string",
      default: "./data/mypal.db",
    },
    databaseMaxOpenConns: {
      type: "integer",
      title: "Max Open Connections",
      description: "Maximum open database connections (0 = unlimited)",
      default: 0,
      minimum: 0,
    },
    databaseMaxIdleConns: {
      type: "integer",
      title: "Max Idle Connections",
      description: "Maximum idle database connections (0 = unlimited)",
      default: 0,
      minimum: 0,
    },

    // ========== MEMORY CONFIGURATION ==========

    // --- Vector Memory (Semantic Search) ---
    memoryVectorEnabled: {
      type: "boolean",
      title: "Vector Memory",
      description: "Enable semantic vector search for memory recall (Qdrant or pgvector)",
      default: false,
    },
    memoryVectorBackend: {
      type: "string",
      title: "Vector Backend",
      description: "Vector store provider",
      enum: ["qdrant", "pgvector"],
      default: "qdrant",
      dependencies: {
        memoryVectorEnabled: {
          properties: { memoryVectorEnabled: { const: true } },
        },
      },
    },
    memoryVectorTopK: {
      type: "integer",
      title: "Top K Results",
      description: "Number of similar memories to retrieve per query",
      default: 10,
      minimum: 1,
      maximum: 100,
      dependencies: {
        memoryVectorEnabled: {
          properties: { memoryVectorEnabled: { const: true } },
        },
      },
    },
    memoryVectorQdrantEndpoint: {
      type: "string",
      title: "Qdrant Endpoint",
      description: "Qdrant REST API endpoint",
      default: "http://localhost:6333",
      dependencies: {
        memoryVectorBackend: {
          properties: { memoryVectorBackend: { const: "qdrant" } },
        },
      },
    },
    memoryVectorQdrantCollection: {
      type: "string",
      title: "Qdrant Collection",
      description: "Qdrant collection name for memories",
      default: "mypal_memories",
      dependencies: {
        memoryVectorBackend: {
          properties: { memoryVectorBackend: { const: "qdrant" } },
        },
      },
    },
    memoryVectorQdrantApiKey: {
      type: "string",
      title: "Qdrant API Key",
      description: "Qdrant API key (optional)",
      format: "password",
      dependencies: {
        memoryVectorBackend: {
          properties: { memoryVectorBackend: { const: "qdrant" } },
        },
      },
    },

    // --- Graph Memory (Entity Relationships) ---
    memoryGraphEnabled: {
      type: "boolean",
      title: "Graph Memory",
      description: "Enable graph-based entity relationship memory (FalkorDB or file)",
      default: false,
    },
    memoryGraphBackend: {
      type: "string",
      title: "Graph Backend",
      description: "Graph store provider",
      enum: ["falkordb", "file"],
      default: "falkordb",
      dependencies: {
        memoryGraphEnabled: {
          properties: { memoryGraphEnabled: { const: true } },
        },
      },
    },
    memoryGraphFalkordbAddr: {
      type: "string",
      title: "FalkorDB Address",
      description: "FalkorDB Redis-compatible address (host:port)",
      default: "localhost:6379",
      dependencies: {
        memoryGraphBackend: {
          properties: { memoryGraphBackend: { const: "falkordb" } },
        },
      },
    },
    memoryGraphFalkordbPassword: {
      type: "string",
      title: "FalkorDB Password",
      description: "FalkorDB password (optional)",
      format: "password",
      dependencies: {
        memoryGraphBackend: {
          properties: { memoryGraphBackend: { const: "falkordb" } },
        },
      },
    },
    memoryGraphFalkordbGraph: {
      type: "string",
      title: "FalkorDB Graph Name",
      description: "FalkorDB graph name for memory entities",
      default: "mypal_memory",
      dependencies: {
        memoryGraphBackend: {
          properties: { memoryGraphBackend: { const: "falkordb" } },
        },
      },
    },
    memoryGraphFilePath: {
      type: "string",
      title: "Graph File Path",
      description: "Path for file-based graph storage (JSON format)",
      default: "data/graph.json",
      dependencies: {
        memoryGraphBackend: {
          properties: { memoryGraphBackend: { const: "file" } },
        },
      },
    },

    // ========== SUBAGENTS CONFIGURATION ==========
    subagentsMaxConcurrent: {
      type: "integer",
      title: "Max Concurrent Subagents",
      description: "Maximum number of concurrent subagents",
      default: 3,
      minimum: 1,
      maximum: 10,
      dependencies: {
        "capabilities.subagents": {
          properties: {
            capabilities: {
              properties: { subagents: { const: true } },
            },
          },
        },
      },
    },
    subagentsDefaultTimeout: {
      type: "string",
      title: "Default Timeout",
      description: "Default timeout for subagent tasks (e.g., 5m)",
      default: "5m",
      pattern: "^\\d+[smh]$",
      dependencies: {
        "capabilities.subagents": {
          properties: {
            capabilities: {
              properties: { subagents: { const: true } },
            },
          },
        },
      },
    },

    // ========== GRAPHQL CONFIGURATION ==========
    graphqlEnabled: {
      type: "boolean",
      title: "GraphQL Enabled",
      description: "Enable GraphQL API server",
      default: true,
    },
    graphqlPort: {
      type: "integer",
      title: "GraphQL Port",
      description: "Port for GraphQL server",
      default: 8080,
      minimum: 1024,
      maximum: 65535,
      dependencies: {
        graphqlEnabled: {
          properties: { graphqlEnabled: { const: true } },
        },
      },
    },
    graphqlHost: {
      type: "string",
      title: "GraphQL Host",
      description: "Host for GraphQL server",
      default: "127.0.0.1",
      dependencies: {
        graphqlEnabled: {
          properties: { graphqlEnabled: { const: true } },
        },
      },
    },
    graphqlBaseUrl: {
      type: "string",
      title: "Server Base URL",
      description: "Public URL of the server (e.g. https://mypal.example.com). Used for OAuth callbacks and MCP.",
      default: "",
      dependencies: {
        graphqlEnabled: {
          properties: { graphqlEnabled: { const: true } },
        },
      },
    },

    // ========== LOGGING CONFIGURATION ==========
    loggingLevel: {
      type: "string",
      title: "Log Level",
      description: "Logging verbosity level",
      enum: ["debug", "info", "warn", "error"],
      default: "info",
    },
    loggingPath: {
      type: "string",
      title: "Log Path",
      description: "Directory for log files",
      default: "./logs",
    },

    // ========== SECRETS CONFIGURATION ==========
    secretsBackend: {
      type: "string",
      title: "Secrets Backend",
      description: "Where agent secrets and credentials are stored",
      enum: ["file", "openbao"],
      default: "file",
    },
    secretsFilePath: {
      type: "string",
      title: "Secrets File Path",
      description: "Path to secrets file",
      default: "./data/secrets.json",
      dependencies: {
        secretsBackend: {
          properties: { secretsBackend: { const: "file" } },
        },
      },
    },
    secretsOpenbaoURL: {
      type: "string",
      title: "OpenBao URL",
      description: "OpenBao server URL",
      dependencies: {
        secretsBackend: {
          properties: { secretsBackend: { const: "openbao" } },
        },
      },
    },
    secretsOpenbaoToken: {
      type: "string",
      title: "OpenBao Token",
      description: "OpenBao authentication token",
      format: "password",
      dependencies: {
        secretsBackend: {
          properties: { secretsBackend: { const: "openbao" } },
        },
      },
    },
    configEncryptionEnabled: {
      type: "boolean",
      title: "Config File Encryption",
      description: "Encrypt the config file on disk (uses AES-GCM). Disable for plain YAML.",
      default: false,
    },

    // ========== SCHEDULER CONFIGURATION ==========
    schedulerEnabled: {
      type: "boolean",
      title: "Scheduler Enabled",
      description: "Enable the task scheduler event loop",
      default: true,
    },
    schedulerMemoryEnabled: {
      type: "boolean",
      title: "Memory Consolidation",
      description: "Enable periodic memory consolidation",
      default: true,
    },
    schedulerMemoryInterval: {
      type: "string",
      title: "Consolidation Interval",
      description: "How often to run memory consolidation (e.g. \"4h\")",
      default: "4h",
    },

    // ========== CHANNEL CONFIGURATION ==========
    channelTelegramEnabled: {
      type: "boolean",
      title: "Enable Telegram",
      description: "Activate the Telegram bot channel",
      default: false,
    },
    channelTelegramToken: {
      type: "string",
      title: "Telegram Bot Token",
      description: "Bot token from @BotFather",
      format: "password",
      dependencies: {
        channelTelegramEnabled: {
          properties: { channelTelegramEnabled: { const: true } },
        },
      },
    },
    channelDiscordEnabled: {
      type: "boolean",
      title: "Enable Discord",
      description: "Activate the Discord bot channel",
      default: false,
    },
    channelDiscordToken: {
      type: "string",
      title: "Discord Bot Token",
      description: "Bot token from the Discord Developer Portal",
      format: "password",
      dependencies: {
        channelDiscordEnabled: {
          properties: { channelDiscordEnabled: { const: true } },
        },
      },
    },
    channelSlackEnabled: {
      type: "boolean",
      title: "Enable Slack",
      description: "Activate the Slack bot channel via Socket Mode",
      default: false,
    },
    channelSlackBotToken: {
      type: "string",
      title: "Slack Bot Token",
      description: "Bot OAuth token (xoxb-...)",
      format: "password",
      dependencies: {
        channelSlackEnabled: {
          properties: { channelSlackEnabled: { const: true } },
        },
      },
    },
    channelSlackAppToken: {
      type: "string",
      title: "Slack App-Level Token",
      description: "App-level token for Socket Mode (xapp-...)",
      format: "password",
      dependencies: {
        channelSlackEnabled: {
          properties: { channelSlackEnabled: { const: true } },
        },
      },
    },
    channelWhatsAppEnabled: {
      type: "boolean",
      title: "Enable WhatsApp",
      description: "Activate the WhatsApp Business API channel",
      default: false,
    },
    channelWhatsAppPhoneId: {
      type: "string",
      title: "WhatsApp Phone ID",
      description: "Phone Number ID from Meta Business Suite",
      dependencies: {
        channelWhatsAppEnabled: {
          properties: { channelWhatsAppEnabled: { const: true } },
        },
      },
    },
    channelWhatsAppApiToken: {
      type: "string",
      title: "WhatsApp API Token",
      description: "Permanent or temporary access token from Meta Business Suite",
      format: "password",
      dependencies: {
        channelWhatsAppEnabled: {
          properties: { channelWhatsAppEnabled: { const: true } },
        },
      },
    },
    channelTwilioEnabled: {
      type: "boolean",
      title: "Enable Twilio (SMS)",
      description: "Activate the Twilio SMS channel",
      default: false,
    },
    channelTwilioAccountSid: {
      type: "string",
      title: "Twilio Account SID",
      description: "Account SID from the Twilio Console",
      dependencies: {
        channelTwilioEnabled: {
          properties: { channelTwilioEnabled: { const: true } },
        },
      },
    },
    channelTwilioAuthToken: {
      type: "string",
      title: "Twilio Auth Token",
      description: "Auth token from the Twilio Console",
      format: "password",
      dependencies: {
        channelTwilioEnabled: {
          properties: { channelTwilioEnabled: { const: true } },
        },
      },
    },
    channelTwilioFromNumber: {
      type: "string",
      title: "Twilio From Number",
      description: "Twilio phone number used to send messages (E.164, e.g. +15551234567)",
      dependencies: {
        channelTwilioEnabled: {
          properties: { channelTwilioEnabled: { const: true } },
        },
      },
    },
  },
};

/**
 * Maps schema field keys to i18n keys. Used by SchemaField to render translated labels.
 * Key format: settings.field.<fieldKey> for title, settings.field.<fieldKey>Desc for description.
 * Nested keys use underscore: capabilities.browser -> settings.field.capabilities_browser
 */
export function getSchemaFieldI18nKey(field: string, forDescription = false): string {
  const base = `settings.field.${field.replace(/\./g, "_")}`;
  return forDescription ? `${base}Desc` : base;
}

/**
 * Group configuration for organizing settings in the UI
 */
export const configGroups = [
  {
    id: "general",
    title: "GENERAL CONFIGURATION",
    fields: [
      "agentName",
      "provider",
      "model",
      // OpenAI / OpenRouter / OpenCode Zen / OpenAI-compatible
      "apiKey",
      // OpenAI-compatible only
      "baseURL",
      // Ollama
      "ollamaHost",
      "ollamaApiKey",
      // Anthropic
      "anthropicApiKey",
      // Docker Model Runner
      "dockerModelRunnerEndpoint",
    ],
  },
  {
    id: "capabilities",
    title: "AGENT CAPABILITIES",
    fields: ["capabilities"],
  },
  {
    id: "database",
    title: "DATABASE CONFIGURATION",
    fields: ["databaseDriver", "databaseDSN", "databaseMaxOpenConns", "databaseMaxIdleConns"],
  },
  {
    id: "memory",
    title: "MEMORY CONFIGURATION",
    fields: [
      "memoryVectorEnabled", "memoryVectorBackend", "memoryVectorTopK",
      "memoryVectorQdrantEndpoint", "memoryVectorQdrantCollection", "memoryVectorQdrantApiKey",
      "memoryGraphEnabled", "memoryGraphBackend",
      "memoryGraphFalkordbAddr", "memoryGraphFalkordbPassword", "memoryGraphFalkordbGraph", "memoryGraphFilePath",
    ],
  },
  {
    id: "subagents",
    title: "SUBAGENTS CONFIGURATION",
    fields: ["subagentsMaxConcurrent", "subagentsDefaultTimeout"],
  },
  {
    id: "graphql",
    title: "GRAPHQL CONFIGURATION",
    fields: ["graphqlEnabled", "graphqlPort", "graphqlHost", "graphqlBaseUrl"],
  },
  {
    id: "logging",
    title: "LOGGING CONFIGURATION",
    fields: ["loggingLevel", "loggingPath"],
  },
  {
    id: "secrets",
    title: "SECRETS CONFIGURATION",
    fields: ["secretsBackend", "secretsFilePath", "secretsOpenbaoURL", "secretsOpenbaoToken", "configEncryptionEnabled"],
  },
  {
    id: "scheduler",
    title: "SCHEDULER CONFIGURATION",
    fields: ["schedulerEnabled", "schedulerMemoryEnabled", "schedulerMemoryInterval"],
  },
  {
    id: "channels",
    title: "CHANNEL CONFIGURATION",
    fields: [
      "channelTelegramEnabled",
      "channelTelegramToken",
      "channelDiscordEnabled",
      "channelDiscordToken",
      "channelSlackEnabled",
      "channelSlackBotToken",
      "channelSlackAppToken",
      "channelWhatsAppEnabled",
      "channelWhatsAppPhoneId",
      "channelWhatsAppApiToken",
      "channelTwilioEnabled",
      "channelTwilioAccountSid",
      "channelTwilioAuthToken",
      "channelTwilioFromNumber",
    ],
  },
];
