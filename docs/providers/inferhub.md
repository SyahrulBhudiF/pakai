# InferHub

Track your InferHub account balance and spend through the [account usage API](https://inferhub.dev).

## What it tracks

InferHub is an [OI]/Anthropic-compatible inference gateway. pakai reads your USDC balance and the spend over the last 30 days from the InferHub management API and shows two windows:

- `30d` — spend over the last 30 days
- `bal` — your current USDC balance

The spend window shows a percentage only once you set a budget. Without a budget, pakai shows the raw dollar figures.

The window rolls forward instead of resetting, so pakai does not show a reset countdown or a burn-rate projection for it.

## Credential source

Create an inference key in the InferHub dashboard. It starts with `sk-airo-`.

Store it with hidden input so the key never reaches your shell history or the process list:

```bash
pakai setup inferhub
```

`pakai config set provider.inferhub.key` with no value prompts the same way.

To pass the key directly instead, which is convenient in a script but leaves it visible in the process list while the command runs:

```bash
pakai config set provider.inferhub.key sk-airo-...
```

Either form stores the key, sets the config file to mode 600, and confirms with a masked value such as `sk-air...kT7O`. The key itself is never printed. Setting the key also enables the provider: InferHub is opt-in, so pakai polls it only when a key is configured and it never appears as an error tile for users who do not use it.

Reading the key back is masked too, so it does not land in terminal scrollback:

```bash
pakai config get provider.inferhub.key          # provider.inferhub.key = sk-air...kT7O
pakai config get provider.inferhub.key --raw    # prints the full key
```

## Enable

```bash
pakai setup inferhub
pakai config set provider.inferhub.limit 20
pakai daemon restart
```

The `limit` is your 30-day budget in USDC. The percentage is spend against that budget; the balance is always shown as a separate raw figure.

## Changing settings

The provider reads the key, label, and limit at startup. After changing any of them, restart the daemon:

```bash
pakai daemon restart
```
