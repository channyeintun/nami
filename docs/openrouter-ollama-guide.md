# OpenRouter and Ollama Guide

This guide shows how to use `gocode` with:

- OpenRouter free models
- Your local Ollama model: `gemma4-e4b`

## Important limitation

`openrouter` is **not** a built-in provider name in the current code.

That means you should **not** use model names like:

```bash
gocode --model openrouter/google/gemma-3-27b-it:free
```

That will not resolve correctly.

Instead, use the **OpenAI-compatible provider** with OpenRouter's base URL:

- provider prefix: `openai/`
- base URL: `https://openrouter.ai/api/v1`
- API key: `GOCLI_API_KEY` or `OPENAI_API_KEY`

## Option 1: OpenRouter free models

### One-off shell session

```bash
export GOCLI_API_KEY="your-openrouter-api-key"
export GOCLI_BASE_URL="https://openrouter.ai/api/v1"

gocode --model openai/google/gemma-3-27b-it:free
```

You can also use `OPENAI_API_KEY` instead of `GOCLI_API_KEY`:

```bash
export OPENAI_API_KEY="your-openrouter-api-key"
export GOCLI_BASE_URL="https://openrouter.ai/api/v1"

gocode --model openai/google/gemma-3-27b-it:free
```

### Why the `openai/` prefix is required

The CLI parses models as `provider/model`.

OpenRouter model IDs often contain slashes, for example:

```text
google/gemma-3-27b-it:free
```

If you pass that directly, the CLI would treat `google` as the provider, which is not supported.

This works correctly:

```bash
gocode --model openai/google/gemma-3-27b-it:free
```

Because the CLI then uses:

- provider: `openai`
- model: `google/gemma-3-27b-it:free`

That matches the current implementation.

### Suggested OpenRouter free models

Examples you can try:

```bash
gocode --model openai/google/gemma-3-27b-it:free
gocode --model openai/meta-llama/llama-3.3-70b-instruct:free
gocode --model openai/mistralai/mistral-small-3.1-24b-instruct:free
```

Model availability changes on OpenRouter, so check their current free model list.

## Option 2: Local Ollama with Gemma 4 e4b

There are two different ways to use Ollama in this project:

1. Keep your main model remote, but allow Ollama for helper tasks
2. Use Ollama as the main interactive model

Those are different setups.

## What `USE_LOCAL_MODEL` actually does

If you set:

```bash
export USE_LOCAL_MODEL=true
```

the CLI is allowed to use Ollama for internal helper tasks that are wired to the local router.

Right now, in the current codebase, that means:

- compaction only

It does **not** change your main interactive model.

Without that variable, the CLI will keep helper tasks on the remote model.

### Start Ollama and confirm the model exists

```bash
ollama list
```

You should see something like:

```text
gemma4-e4b
```

If you do not have it yet:

```bash
ollama pull gemma4-e4b
```

### Setup A: Keep OpenRouter as the main model, use Ollama only for compaction

```bash
export GOCLI_API_KEY="your-openrouter-api-key"
export GOCLI_BASE_URL="https://openrouter.ai/api/v1"
export USE_LOCAL_MODEL=true

gocode --model openai/google/gemma-3-27b-it:free
```

With that setup:

- main interactive model: OpenRouter
- helper task routing: Ollama is allowed
- current helper task using Ollama: compaction

### Setup B: Run `go-cli` with Ollama as the main model

```bash
gocode --model ollama/gemma4-e4b
```

The built-in Ollama base URL is already:

```text
http://localhost:11434
```

So you usually do not need to set anything else.

This changes the main interactive model to Ollama.

### If Ollama is on a different host or port

```bash
export GOCLI_BASE_URL="http://127.0.0.1:11434"
gocode --model ollama/gemma4-e4b
```

## Switching between OpenRouter and Ollama

The main thing to avoid is leaving `GOCLI_BASE_URL` set to OpenRouter when you want to use Ollama.

### OpenRouter session

```bash
export GOCLI_API_KEY="your-openrouter-api-key"
export GOCLI_BASE_URL="https://openrouter.ai/api/v1"
export USE_LOCAL_MODEL=true
gocode --model openai/google/gemma-3-27b-it:free
```

This is the setup to use if you want OpenRouter as the main model and Ollama only for compaction.

### Ollama session

```bash
unset GOCLI_API_KEY
unset OPENAI_API_KEY
unset GOCLI_BASE_URL
unset USE_LOCAL_MODEL
gocode --model ollama/gemma4-e4b
```

This is the setup to use if you want Ollama as the main model.

## Recommended shell aliases

Add these to your shell config if you switch often.

### zsh / bash

```bash
alias gocode-openrouter='GOCLI_BASE_URL="https://openrouter.ai/api/v1" gocode --model openai/google/gemma-3-27b-it:free'
alias gocode-ollama='env -u GOCLI_BASE_URL -u GOCLI_API_KEY -u OPENAI_API_KEY gocode --model ollama/gemma4-e4b'
```

If you want the OpenRouter alias to also opt into local compaction, use this version instead:

```bash
alias gocode-openrouter='USE_LOCAL_MODEL=true GOCLI_BASE_URL="https://openrouter.ai/api/v1" gocode --model openai/google/gemma-3-27b-it:free'
```

If you use the OpenRouter alias, make sure your API key is already exported in your shell profile.

Example:

```bash
export GOCLI_API_KEY="your-openrouter-api-key"
```

## Recommended config file setups

Config file path:

```text
~/.config/go-cli/config.json
```

### OpenRouter-focused config

```json
{
  "model": "openai/google/gemma-3-27b-it:free",
  "base_url": "https://openrouter.ai/api/v1",
  "default_mode": "plan"
}
```

Then just export your API key:

```bash
export GOCLI_API_KEY="your-openrouter-api-key"
export USE_LOCAL_MODEL=true
gocode
```

That keeps OpenRouter as the main model and allows Ollama for compaction.

### Ollama-focused config

```json
{
  "model": "ollama/gemma4-e4b",
  "default_mode": "plan"
}
```

Then just run:

```bash
gocode
```

That uses Ollama as the main model.

## Fast troubleshooting

### Error: unsupported provider

Cause:

```bash
gocode --model google/gemma-3-27b-it:free
```

Fix:

```bash
gocode --model openai/google/gemma-3-27b-it:free
```

### Error: missing API key for provider "openai"

Cause:

You are using OpenRouter through the OpenAI-compatible client, but no API key is set.

Fix:

```bash
export GOCLI_API_KEY="your-openrouter-api-key"
```

or:

```bash
export OPENAI_API_KEY="your-openrouter-api-key"
```

### Error: Ollama connection refused

Cause:

Ollama is not running, or `GOCLI_BASE_URL` is still pointing at OpenRouter.

Fix:

```bash
unset GOCLI_BASE_URL
ollama serve
```

Then run:

```bash
gocode --model ollama/gemma4-e4b
```

If you are only trying to use Ollama for compaction while keeping OpenRouter as the main model, do not switch to `ollama/...`; keep your OpenRouter model and set only:

```bash
export USE_LOCAL_MODEL=true
```

## Practical recommendation

If your goal is:

- cheapest hosted option: use OpenRouter free models
- simplest offline/local option: use `ollama/gemma4-e4b`

A good workflow is:

- use OpenRouter free models when you want stronger remote reasoning
- set `USE_LOCAL_MODEL=true` when you want Ollama used for helper tasks like compaction
- use `ollama/gemma4-e4b` as the main model when you want fully local usage or no API spend

Short version:

- `USE_LOCAL_MODEL=true` = opt in local compaction
- `--model ollama/gemma4-e4b` = make Ollama the main model in `gocode`
