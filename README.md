# Xeneon Edge Widget Suite

Xeneon Edge Widget Suite is a small collection of custom web widgets and a shared local Windows bridge for the Corsair Xeneon Edge display ecosystem.

The goal of this repository is to keep widget UI code and Windows-side integration in one place, so new widgets can reuse the same local service instead of each widget needing its own native integration layer.

## What Is In This Repository

- `PowerPad/`
  Contains the Power Pad widget HTML and widget-specific documentation.
- `NetPulse/`
  Contains the Net Pulse network monitor widget HTML and widget-specific documentation.
- `WidgetBridge/`
  Contains the local Go tray bridge that exposes Windows power and utility actions over HTTP.

## Current Status

This repository is currently in a working early version.

- The Power Pad widget is implemented and documented.
- The Net Pulse widget is implemented and documented.
- The widget is meant to be pasted into the iframe widget inside Corsair iCUE for Xeneon Edge.
- The shared WidgetBridge is implemented in Go and runs locally on Windows as a tray app.
- The bridge currently supports lock, sleep, restart, shutdown, Task Manager, and Power Settings actions.
- The bridge also exposes live network throughput and ping metrics for monitoring widgets.
- The bridge also supports `--stop` to stop a running bridge instance cleanly.
- The tray can disable or re-enable the bridge and shows which widgets are currently available.
- The repository is structured so additional widgets can be added without changing the overall layout.

## How It Works

1. A widget HTML file is authored in this repository.
2. The widget calls the local bridge at `http://127.0.0.1:39291`.
3. The Go bridge receives those requests and launches the corresponding Windows actions.
4. Multiple widgets can reuse the same local bridge process.
5. The tray menu can temporarily disable the bridge or exit it completely.

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

### Net Pulse

Net Pulse is a compact network monitor for live throughput and latency.

Current behavior:

- Download and upload throughput update from the local bridge.
- Ping is measured against the bridge target, default `1.1.1.1`.
- The active adapter name is shown in the fourth card.
- The footer shows bridge connectivity and the current ping target.

See `NetPulse/README.md` for widget-specific usage instructions.

### WidgetBridge

WidgetBridge is the shared local tray service used by widgets in this repository.

Current behavior:

- Exposes a `/health` endpoint for widget connectivity checks.
- Exposes a `/network/status` endpoint for live network telemetry.
- Exposes `/action/...` routes for Windows actions.
- Uses a single local HTTP address by default: `127.0.0.1:39291`.
- Can be stopped with `widgetbridge.exe --stop`.
- Shows bridge state and available widgets in the Windows tray.

See `WidgetBridge/README.md` for bridge-specific details.

## Getting Started

### 1. Start the bridge

```powershell
cd C:\Users\filip\Programming\XeneonWidgets\WidgetBridge
go run .
```

For a terminal-free tray executable on Windows, build it with:

```powershell
cd C:\Users\filip\Programming\XeneonWidgets\WidgetBridge
.\build.ps1
```

To build the installer that places WidgetBridge in `C:\Program Files\WidgetBridge` and enables startup:

```powershell
cd C:\Users\filip\Programming\XeneonWidgets\WidgetBridge
.\build-installer.ps1
```

That script builds only `widgetbridge-installer.exe`.

Or run the built executable:

```powershell
cd C:\Users\filip\Programming\XeneonWidgets\WidgetBridge
.\widgetbridge.exe
```

Or run the installer executable:

```powershell
cd C:\Users\filip\Programming\XeneonWidgets\WidgetBridge
.\widgetbridge-installer.exe
```

If `widgetbridge.exe` is not already present in the `WidgetBridge` folder, the installer now tries to build it automatically before installing.

The built executable appears as a tray icon without opening a console window. From there you can disable the bridge, inspect the currently available widgets, or fully exit it.

### 2. Load the widget into Corsair iCUE

Open `PowerPad/power_pad.html`, copy the full file content, and paste it into the iframe widget inside the Corsair iCUE app for Xeneon Edge.

You can repeat the same flow with `NetPulse/net_pulse.html` for the network monitor widget.

## Repository Layout

```text
XeneonWidgets/
  NetPulse/
    net_pulse.html
    README.md
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