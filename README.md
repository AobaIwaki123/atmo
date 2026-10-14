# atmo

A lightweight, zero-dependency barometric pressure CLI, PNG chart renderer, and Discord notifier written in Go.

Powered by [Open-Meteo](https://open-meteo.com/) free weather API (no API key required).

## Features

- **Terminal 2D ASCII Chart**: Instant visual curves with a dynamic "NOW" time cursor.
- **Discord-Optimized PNG Chart Generator**: Built-in 800x420 dark-mode PNG renderer. Zero external graphics dependencies (no ImageMagick, no Python).
- **Discord Webhook Support**: Automatically posts rich Embeds and attached PNG charts via `multipart/form-data`.
- **3-Hour Barometric Delta ($\Delta P$)**: Evaluates weather-headache risks ($\Delta \ge 3.0$ hPa triggers `⚠️ CAUTION: 急変警告`).
- **Kubernetes Ready**: Includes multi-stage Dockerfile and `k8s/cronjob.yaml`.

## Installation

```bash
go install github.com/AobaIwaki123/atmo@latest
```

Or build from source:

```bash
git clone https://github.com/AobaIwaki123/atmo.git
cd atmo
go build -o atmo .
```

## Usage

### 1. Terminal ASCII Chart (Default)

```bash
# Tokyo default (12h past, 24h future)
atmo

# Custom window
atmo -before 6 -ahead 12
```

### 2. Export PNG Chart

```bash
atmo -out chart.png
```

### 3. Post Directly to Discord Webhook

```bash
# Via CLI flag
atmo -webhook "https://discord.com/api/webhooks/..."

# Or via Environment Variable
export DISCORD_WEBHOOK_URL="https://discord.com/api/webhooks/..."
atmo
```

## Kubernetes Deployment (CronJob)

1. Create Discord Webhook secret:
```bash
kubectl create secret generic atmo-secrets \
  --from-literal=discord-webhook-url="https://discord.com/api/webhooks/..."
```

2. Apply CronJob (runs hourly):
```bash
kubectl apply -f k8s/cronjob.yaml
```
