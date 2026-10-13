# Providers

pakai supports the following usage data sources. Each provider has its own page with prerequisites, configuration, and a result screenshot.

| Provider | ID | Source | Windows | Auth required |
|---|---|---|---|---|
| [Claude](claude.md) | `claude` | OAuth API + `~/.claude/stats-cache.json` | 5h, weekly | `claude login` |
| [OpenAI Codex](codex.md) | `openai` | Pi OAuth or Codex CLI OAuth | 5h, weekly | Pi or Codex CLI login |
| [OpenCode (local)](opencode.md) | `opencode` | `~/.local/share/opencode/*.db` SQLite | 5h, weekly, monthly | opencode.ai login |
| [OpenCode Go](opencode-go.md) | `opencode-go` | OpenCode Usage API | 5h, weekly, monthly | Pi or OpenCode API key |
| [Pi](pi.md) | `pi/<provider>` | `~/.pi/agent/sessions/**/*.jsonl` | Monthly (local tokens) | Use Pi |
| [InferHub](inferhub.md) | `inferhub` | InferHub account usage API | 30d spend, balance | InferHub API key |

## Quick config reference

```bash
# Enable / disable a provider
pakai config set provider.<id>.enabled true
pakai config set provider.<id>.enabled false

# Set a dollar or message limit (turns raw usage into a percentage)
pakai config set provider.<id>.limit 60

# View current config
pakai config list
```

For provider-specific setup details, follow the links in the table above.
