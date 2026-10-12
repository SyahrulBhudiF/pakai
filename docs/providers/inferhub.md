# InferHub

Track your InferHub account balance and spend through the [account usage API](https://inferhub.dev).

## What it tracks

InferHub is an OpenAI/Anthropic-compatible inference gateway. pakai reads your USDC balance and the spend over the last 30 days from the InferHub management API and shows two windows:

- `30d` — spend over the last 30 days
- `bal` — your current USDC balance

The spend window shows a percentage only once you set a budget. Without a budget, pakai shows the raw dollar figures.

The window rolls forward instead of resetting, so pakai does not show a reset countdown or a burn-rate projection for it.

## Credential source

Create an inference key in the InferHub dashboard. It starts with `sk-airo-`.

```bash
pakai config set provider.inferhub.key sk-airo-...
```

Setting the key also enables the provider. InferHub is opt-in: pakai polls it only when a key is configured, so it never appears as an error tile for users who do not use it.

## Enable

```bash
pakai config set provider.inferhub.key sk-airo-...
pakai config set provider.inferhub.limit 20
pakai daemon restart
```

The `limit` is your 30-day budget in USDC. The percentage is spend against that budget; the balance is always shown as a separate raw figure.

## Changing settings

The provider reads the key, label, and limit at startup. After changing any of them, restart the daemon:

```bash
pakai daemon restart
```
