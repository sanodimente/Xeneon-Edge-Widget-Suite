# Xeneon Edge Widget Suite

Xeneon Edge Widget Suite is a small collection of custom web widgets and a shared local Windows bridge for the Corsair Xeneon Edge display ecosystem.

The goal of this repository is to keep widget UI code and Windows-side integration in one place, so new widgets can reuse the same local service instead of each widget needing its own native integration layer.

## What Is In This Repository

- `PowerPad/`
  Contains the Power Pad widget HTML and widget-specific documentation.
- `WidgetBridge/`
  Contains the local Go bridge that exposes Windows power and utility actions over HTTP.

## Current Status

This repository is currently in a working early version.

- The Power Pad widget is implemented and documented.
- The widget is meant to be pasted into the iframe widget inside Corsair iCUE for Xeneon Edge.
- The shared WidgetBridge is implemented in Go and can be started locally on Windows.
- The bridge currently supports lock, sleep, restart, shutdown, Task Manager, and Power Settings actions.
- The bridge also supports `--stop` to stop a running bridge instance cleanly.
- The repository is structured so additional widgets can be added without changing the overall layout.

## How It Works

1. A widget HTML file is authored in this repository.
2. The widget calls the local bridge at `http://127.0.0.1:39291`.
3. The Go bridge receives those requests and launches the corresponding Windows actions.
4. Multiple widgets can reuse the same local bridge process.

## Current Components

### Power Pad

Power Pad is a compact control surface for common Windows power actions.

Current behavior:

- Lock and Sleep run immediately.
- Restart and Shut Down require confirmation.
- Task Manager and Power Settings are available as footer actions.
- The header shows the current date and time.
- The footer shows bridge connection status.

See `PowerPad/README.md` for widget-specific usage instructions.

### WidgetBridge

WidgetBridge is the shared local service used by widgets in this repository.

Current behavior:

- Exposes a `/health` endpoint for widget connectivity checks.
- Exposes `/action/...` routes for Windows actions.
- Uses a single local HTTP address by default: `127.0.0.1:39291`.
- Can be stopped with `widgetbridge.exe --stop`.

See `WidgetBridge/README.md` for bridge-specific details.

## Getting Started

### 1. Start the bridge

```powershell
cd C:\Users\filip\Programming\XeneonWidgets\WidgetBridge
go run .
```

Or run the built executable:

```powershell
cd C:\Users\filip\Programming\XeneonWidgets\WidgetBridge
.\widgetbridge.exe
```

### 2. Load the widget into Corsair iCUE

Open `PowerPad/power_pad.html`, copy the full file content, and paste it into the iframe widget inside the Corsair iCUE app for Xeneon Edge.

## Repository Layout

```text
XeneonWidgets/
  PowerPad/
    power_pad.html
    README.md
  WidgetBridge/
    go.mod
    main.go
    README.md
```

## Direction

The current direction of the project is:

- keep widgets self-contained in their own folders
- keep WidgetBridge shared across widgets
- add new native actions to the bridge only when a widget actually needs them
- keep the widget side lightweight so it can be embedded easily in Xeneon Edge iframe widgets