<div align="center">

# Synapse

### AI coding agent for the terminal

Multi-provider agentic coding with TUI, tool use, session management,
Engram memory integration, and infrastructure monitoring.

[Quick Start](#quick-start) · [Commands](#commands) · [Providers](#providers) · [Tools](#tools) · [Configuration](#configuration) · [Headless API](#headless-api)
[![License: Elastic-2.0](https://img.shields.io/badge/License-Elastic%202.0-blue)](https://www.elastic.co/licensing/elastic-license)

</div>

---

## What is Synapse?

Synapse is an AI coding agent that runs in your terminal. It connects to multiple LLM providers, gives the model tools to read, edit, and execute code, and remembers context across sessions via [Engram](https://github.com/zanfiel/engram).

```bash
# Start an interactive session
synapse

# Use a specific provider
synapse anthropic
synapse openai

# Run as a headless HTTP server (for Forge or other frontends)
synapse serve :4300
```

**Key features:**

- ⚡ **Multi-provider architecture** — Anthropic API, OpenAI API (and any compatible endpoint) — seamless switching
- 🖥️ **Rich TUI** — BubbleTea-powered terminal interface with themes, markdown rendering, and keyboard shortcuts
- 🛠️ **Agentic tool use** — file read/write/edit, bash execution, git operations, grep, glob, tree, HTTP fetch, undo
- 🧠 **Engram integration** — persistent memory across sessions with automatic context recall and capture
- 🔌 **MCP client** — connect to any Model Context Protocol server for extended tool access
- 💬 **Session management** — save, resume, branch, search, and export conversations (SQLite-backed)
- 🔍 **LSP integration** — auto-detects language servers for diagnostics, hover, go-to-definition, and symbol search
- 🌐 **SSH tool** — execute commands on remote servers directly from the agent
- 🏗️ **Fleet Pulse** — monitor infrastructure health across multiple servers
- 🎯 **Extended thinking** — configurable reasoning budgets for complex tasks
- 📦 **Sub-agents** — spawn child agents for parallel task execution
- 🎨 **Themes** — synapse, tokyo, dracula, gruvbox, catppuccin, nord
- ⚙️ **Per-project config** — `.synapse.json` for project-specific system prompts, custom tools, and model overrides
- 🔄 **Auto-compact** — automatic context window management with configurable thresholds
- 🧩 **Custom tools** — define project-specific tools in `.synapse.json`
- 📊 **Token tracking** — model-aware context window monitoring with usage stats
- 🆙 **Self-update** — `synapse update` checks GitHub releases and installs in-place

---

## Quick Start

### From source

```bash
git clone https://github.com/zanfiel/synapse.git
cd synapse
make build        # → ./synapse
make install      # → /usr/local/bin/synapse
```

### Cross-compile

```bash
make windows      # → synapse.exe
make linux        # → synapse-linux
make all          # → all platforms
```

### Authenticate

```bash
# Option 1: Anthropic API key
export ANTHROPIC_API_KEY=sk-ant-...

# Option 2: OpenAI API key (or any compatible endpoint)
export OPENAI_API_KEY=sk-...
export OPENAI_BASE_URL=https://api.openai.com/v1  # optional
```

### Connect to Engram (optional)

```bash
export ENGRAM_URL=http://localhost:4200
export ENGRAM_TOKEN=eg_your_key
synapse
# → Engram context is automatically recalled at session start
```

---

## Commands

### CLI

```
synapse                    Start interactive session
synapse serve [addr]       Start headless HTTP server (default :4300)
synapse sessions           List saved sessions
synapse resume <id>        Resume a saved session
synapse branch <id>        Branch a session (fork conversation)
synapse export <id>        Export session as JSON
synapse search <query>     Search past sessions
synapse update             Check for and install updates
synapse debug <message>    Single-shot debug mode (no TUI)
synapse version            Show version
synapse help               Show help
```

### Session Commands

| Command | Description |
|---------|-------------|
| `/quit`, `/exit` | Exit Synapse |
| `/clear` | Clear conversation |
| `/compact` | Compress history |
| `/model [name]` | Show or switch model |
| `/theme [name]` | Show or switch theme |
| `/git` | Show git status |
| `/tasks` | Show background tasks |
| `/cost` | Show session cost estimate |
| `/branch` | Fork conversation at this point |
| `/export` | Export current session |
| `/mcp` | Show MCP server status |
| `/usage` | Show provider usage stats |
| `/help` | Show commands |

| Shortcut | Action |
|----------|--------|
| `Ctrl+F` | Search output |
| `PgUp/PgDn` | Scroll output |
| `Tab` | Auto-complete commands |

---

## Providers

Synapse abstracts multiple LLM providers behind a unified interface. Provider priority (when no explicit provider is set):

1. **Anthropic API** — if `ANTHROPIC_API_KEY` is set
2. **OpenAI API** — if `OPENAI_API_KEY` is set (supports custom base URL via `OPENAI_BASE_URL`)

Override with environment variables:

```bash
SYNAPSE_PROVIDER=anthropic synapse
SYNAPSE_MODEL=claude-sonnet-4-20250514 synapse
```

Or force a provider directly:

```bash
synapse anthropic   # Anthropic API
synapse openai      # OpenAI API
```

---

## Tools

Synapse gives the AI model access to these tools:

### Core

| Tool | Description |
|------|-------------|
| `read` | Read file contents (supports images for vision) |
| `write` | Create or overwrite files |
| `edit` | Surgical find-and-replace editing |
| `patch` | Multi-edit patch (multiple find/replace pairs) |
| `bash` | Execute shell commands |
| `tree` | List directory structure |
| `glob` | Find files matching a pattern |
| `grep` | Search file contents with regex |
| `fetch` | HTTP GET/POST for web content and APIs |
| `think` | Extended reasoning scratchpad |
| `todo` | Task list management |
| `undo` | Undo file changes from this session |

### Git

| Tool | Description |
|------|-------------|
| `git_status` | Show working tree status |
| `git_diff` | Show uncommitted changes |
| `git_commit` | Stage and commit changes |
| `git_log` | Show recent commit history |

### Infrastructure

| Tool | Description |
|------|-------------|
| `ssh` | Execute commands on remote servers |
| `memory_store` | Store memories in Engram |
| `memory_search` | Search memories |
| `memory_list` | List recent memories |
| `memory_update` | Update a memory |
| `memory_delete` | Delete a memory |
| `memory_archive` | Archive a memory |
| `conversation_search` | Search past conversations |

### LSP (auto-detected)

| Tool | Description |
|------|-------------|
| `diagnostics` | Get compiler/linter errors |
| `symbol` | Search workspace symbols |
| `hover` | Get type info at a position |
| `definition` | Go to definition |

### Custom Tools

Define project-specific tools in `.synapse.json`:

```json
{
  "tools": [
    {
      "name": "deploy",
      "description": "Deploy to staging",
      "command": "bash deploy.sh staging"
    }
  ]
}
```

---

## Architecture

```
┌─────────────────────────────────────────────────────┐
│                     Synapse                         │
│                                                     │
│  ┌──────────┐  ┌──────────┐  ┌──────────────────┐  │
│  │   TUI    │  │  Agent   │  │  Prompt Engine   │  │
│  │(Bubble   │  │  Loop    │  │  (dynamic sys    │  │
│  │  Tea)    │  │          │  │   prompt build)  │  │
│  └────┬─────┘  └────┬─────┘  └────────┬─────────┘  │
│       │              │                 │            │
│  ┌────┴──────────────┴─────────────────┴────────┐   │
│  │              Provider Layer                  │   │
│  │         Anthropic  │  OpenAI                 │   │
│  └──────────────────────────────────────────────┘   │
│                                                     │
│  ┌──────────┐  ┌──────────┐  ┌──────────────────┐  │
│  │  Tool    │  │  Session  │  │    Engram        │  │
│  │  Registry│  │  Store    │  │    Client         │  │
│  │  (30+)   │  │  (SQLite) │  │    (memory)      │  │
│  └──────────┘  └──────────┘  └──────────────────┘  │
│                                                     │
│  ┌──────────┐  ┌──────────┐  ┌──────────────────┐  │
│  │   LSP    │  │   MCP    │  │   Fleet Pulse    │  │
│  │  Client  │  │  Client  │  │   (infra mon.)   │  │
│  └──────────┘  └──────────┘  └──────────────────┘  │
└─────────────────────────────────────────────────────┘
```

- **Runtime:** Go 1.24+
- **TUI:** BubbleTea + Glamour (markdown) + Lipgloss (styling)
- **Database:** SQLite (session storage via modernc.org/sqlite)
- **Tokens:** tiktoken-go for accurate token counting
- **SSH:** Pooled connections with configurable timeouts

---

## Configuration

### Global Config

Located at `~/.config/synapse/config.json` (Linux/macOS) or `~/.synapse/config.json` (Windows).

```json
{
  "model": "claude-sonnet-4-20250514",
  "provider": "anthropic",
  "anthropic_api_key": "sk-ant-...",
  "engram_url": "http://localhost:4200",
  "engram_token": "eg_...",
  "max_tokens": 8192,
  "reasoning_level": "medium",
  "theme": "synapse",
  "auto_capture": true,
  "fleet_enabled": true,
  "fleet_interval": 300,
  "fleet_servers": [
    {
      "name": "web-1",
      "host": "10.0.0.1",
      "user": "deploy",
      "key_path": "~/.ssh/id_rsa"
    }
  ],
  "mcp_servers": [
    {
      "name": "engram",
      "command": "node",
      "args": ["path/to/mcp-server.mjs"],
      "transport": "stdio"
    }
  ]
}
```

### Project Config

Create `.synapse.json` in any project directory:

```json
{
  "model": "claude-sonnet-4-20250514",
  "system_prompt": "@AGENTS.md",
  "reasoning_level": "high",
  "tools": [
    {
      "name": "test",
      "description": "Run test suite",
      "command": "go test ./..."
    }
  ]
}
```

### Environment Variables

| Variable | Description |
|----------|-------------|
| `SYNAPSE_PROVIDER` | Override provider (`anthropic`, `openai`) |
| `SYNAPSE_MODEL` | Override model name |
| `ANTHROPIC_API_KEY` | Anthropic API key |
| `OPENAI_API_KEY` | OpenAI API key |
| `OPENAI_BASE_URL` | Custom OpenAI-compatible API base URL |
| `ENGRAM_URL` | Engram memory server URL |
| `ENGRAM_TOKEN` | Engram API token |

---

## Headless API

Run `synapse serve` to start an HTTP server for programmatic access. Used by [Forge](https://github.com/zanfiel/forge) and other frontends.

```bash
synapse serve :4300
```

### Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/v1/chat` | Send a message, get a response |
| `POST` | `/v1/chat/stream` | Send a message, get SSE stream |

### Chat Request

```bash
curl -X POST http://localhost:4300/v1/chat \
  -H "Content-Type: application/json" \
  -d '{"message": "What files are in this directory?", "session_id": "my-session"}'
```

### Streaming

```bash
curl -N http://localhost:4300/v1/chat/stream \
  -H "Content-Type: application/json" \
  -d '{"message": "Explain this codebase", "session_id": "my-session"}'
# → SSE events: text, tool_call, tool_result, thinking, done
```

---

## Comparison

| Feature | Synapse | Claude Code | Aider | Continue |
|---------|---------|------------|-------|----------|
| **Multi-provider** | ✅ (2 providers) | ❌ (Anthropic only) | ✅ | ✅ |
| Rich TUI | ✅ | ✅ | ✅ | ❌ (IDE) |
| Persistent memory (Engram) | ✅ | ❌ | ❌ | ❌ |
| Session save/resume/branch | ✅ | ❌ | ❌ | ❌ |
| Conversation search | ✅ | ❌ | ❌ | ❌ |
| LSP integration | ✅ | ❌ | ❌ | ✅ |
| MCP client | ✅ | ✅ | ❌ | ❌ |
| SSH tool | ✅ | ❌ | ❌ | ❌ |
| Infrastructure monitoring | ✅ | ❌ | ❌ | ❌ |
| Sub-agents | ✅ | ✅ | ❌ | ❌ |
| Headless HTTP API | ✅ | ❌ | ❌ | ❌ |
| Custom tools (per-project) | ✅ | ❌ | ❌ | ✅ |
| Extended thinking | ✅ | ✅ | ❌ | ❌ |
| Themes | ✅ (6) | ❌ | ❌ | ✅ |
| Auto-compact | ✅ | ✅ | ✅ | ❌ |
| Self-update | ✅ | ✅ | ✅ | ❌ |
| Self-hosted | ✅ | ❌ | ✅ | ✅ |
| Single binary | ✅ | ❌ | ❌ | ❌ |

---

## License

Elastic License 2.0 — see [LICENSE](LICENSE).
