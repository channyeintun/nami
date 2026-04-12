# OpenRouter and Ollama Guide

This guide shows how to use `chan` with:

- OpenRouter free models
- Your local Ollama model: `gemma4-e4b`

## Important limitation

`openrouter` is **not** a built-in provider name in the current code.

That means you should **not** use model names like:

```bash
chan --model openrouter/google/gemma-3-27b-it:free
```

That will not resolve correctly.

Instead, use the **OpenAI-compatible provider** with OpenRouter's base URL:

- provider prefix: `openai/`
- base URL: `https://openrouter.ai/api/v1`
- API key: `CHAN_API_KEY` or `OPENAI_API_KEY`

## Option 1: OpenRouter free models

### One-off shell session

```bash
export CHAN_API_KEY="your-openrouter-api-key"
export CHAN_BASE_URL="https://openrouter.ai/api/v1"

chan --model openai/google/gemma-3-27b-it:free
```

You can also use `OPENAI_API_KEY` instead of `CHAN_API_KEY`:

```bash
export OPENAI_API_KEY="your-openrouter-api-key"
export CHAN_BASE_URL="https://openrouter.ai/api/v1"

chan --model openai/google/gemma-3-27b-it:free
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
chan --model openai/google/gemma-3-27b-it:free
```

Because the CLI then uses:

- provider: `openai`
- model: `google/gemma-3-27b-it:free`

That matches the current implementation.

### Suggested OpenRouter free models

Examples you can try:

```bash
chan --model openai/google/gemma-3-27b-it:free
chan --model openai/meta-llama/llama-3.3-70b-instruct:free
chan --model openai/mistralai/mistral-small-3.1-24b-instruct:free
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
export CHAN_API_KEY="your-openrouter-api-key"
export CHAN_BASE_URL="https://openrouter.ai/api/v1"
export USE_LOCAL_MODEL=true

chan --model openai/google/gemma-3-27b-it:free
```

With that setup:

- main interactive model: OpenRouter
- helper task routing: Ollama is allowed
- current helper task using Ollama: compaction

### Setup B: Run `chan` with Ollama as the main model

```bash
chan --model ollama/gemma4-e4b
```

The built-in Ollama base URL is already:

```text
http://localhost:11434
```

So you usually do not need to set anything else.

This changes the main interactive model to Ollama.

### If Ollama is on a different host or port

```bash
export CHAN_BASE_URL="http://127.0.0.1:11434"
chan --model ollama/gemma4-e4b
```

## Switching between OpenRouter and Ollama

The main thing to avoid is leaving `CHAN_BASE_URL` set to OpenRouter when you want to use Ollama.

### OpenRouter session

```bash
export CHAN_API_KEY="your-openrouter-api-key"
export CHAN_BASE_URL="https://openrouter.ai/api/v1"
export USE_LOCAL_MODEL=true
chan --model openai/google/gemma-3-27b-it:free
```

This is the setup to use if you want OpenRouter as the main model and Ollama only for compaction.

### Ollama session

```bash
unset CHAN_API_KEY
unset OPENAI_API_KEY
unset CHAN_BASE_URL
unset USE_LOCAL_MODEL
chan --model ollama/gemma4-e4b
```

This is the setup to use if you want Ollama as the main model.

## Recommended shell aliases

Add these to your shell config if you switch often.

### zsh / bash

```bash
alias chan-openrouter='CHAN_BASE_URL="https://openrouter.ai/api/v1" chan --model openai/google/gemma-3-27b-it:free'
alias chan-ollama='env -u CHAN_BASE_URL -u CHAN_API_KEY -u OPENAI_API_KEY chan --model ollama/gemma4-e4b'
```

If you want the OpenRouter alias to also opt into local compaction, use this version instead:

```bash
alias chan-openrouter='USE_LOCAL_MODEL=true CHAN_BASE_URL="https://openrouter.ai/api/v1" chan --model openai/google/gemma-3-27b-it:free'
```

If you use the OpenRouter alias, make sure your API key is already exported in your shell profile.

Example:

```bash
export CHAN_API_KEY="your-openrouter-api-key"
```

## Recommended config file setups

Config file path:

```text
~/.config/chan/config.json
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
export CHAN_API_KEY="your-openrouter-api-key"
export USE_LOCAL_MODEL=true
chan
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
chan
```

That uses Ollama as the main model.

## Fast troubleshooting

### Error: unsupported provider

Cause:

```bash
chan --model google/gemma-3-27b-it:free
```

Fix:

```bash
chan --model openai/google/gemma-3-27b-it:free
```

### Error: missing API key for provider "openai"

Cause:

You are using OpenRouter through the OpenAI-compatible client, but no API key is set.

Fix:

```bash
export CHAN_API_KEY="your-openrouter-api-key"
```

or:

```bash
export OPENAI_API_KEY="your-openrouter-api-key"
```

### Error: Ollama connection refused

Cause:

Ollama is not running, or `CHAN_BASE_URL` is still pointing at OpenRouter.

Fix:

```bash
unset CHAN_BASE_URL
ollama serve
```

Then run:

```bash
chan --model ollama/gemma4-e4b
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
- `--model ollama/gemma4-e4b` = make Ollama the main model in `chan`
