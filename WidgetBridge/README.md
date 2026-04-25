# WidgetBridge

A shared local Go tray app that exposes Windows actions to multiple web widgets loaded in Xeneon Edge.

## Run

From source for development:

```powershell
cd C:\Users\filip\Programming\XeneonWidgets\WidgetBridge
go run .
```

`go run .` is useful for development, but on Windows it still behaves like a console app.

To build the tray-only executable without a terminal window:

```powershell
cd C:\Users\filip\Programming\XeneonWidgets\WidgetBridge
.\build.ps1
```

To build the installer executable:

```powershell
cd C:\Users\filip\Programming\XeneonWidgets\WidgetBridge
.\build-installer.ps1
```

`build-installer.ps1` now builds only the installer executable.

Or use the built executable:

```powershell
cd C:\Users\filip\Programming\XeneonWidgets\WidgetBridge
.\widgetbridge.exe
```

Or install it into `C:\Program Files\WidgetBridge` with startup enabled:

```powershell
cd C:\Users\filip\Programming\XeneonWidgets\WidgetBridge
.\widgetbridge-installer.exe
```

When started from the built executable, WidgetBridge stays in the Windows tray and does not need a visible terminal window.

Tray menu:

- shows whether the bridge is currently ON or OFF
- lets you disable or re-enable the HTTP bridge without closing the tray app
- lists the widgets currently wired to this bridge
- lets you fully exit WidgetBridge

To stop the currently running tray instance from the command line:

```powershell
cd C:\Users\filip\Programming\XeneonWidgets\WidgetBridge
.\widgetbridge.exe --stop
```

The server listens by default on `http://127.0.0.1:39291`.

The installer behavior is:

- requests administrator rights because it writes into `Program Files`
- if `widgetbridge.exe` is missing next to the installer, it tries to build it automatically from the local `WidgetBridge` source folder
- copies `widgetbridge.exe` into `C:\Program Files\WidgetBridge`
- registers autostart for the current Windows user
- launches the installed bridge immediately after setup

The simplest flow is now:

```powershell
cd C:\Users\filip\Programming\XeneonWidgets\WidgetBridge
.\build-installer.ps1
.\widgetbridge-installer.exe
```

Why the terminal could still appear before this change:

- hiding a console from inside the process is only a partial workaround
- a normal `go build .` on Windows still creates a console-subsystem executable
- `build.ps1` compiles WidgetBridge with the Windows GUI subsystem so the console is not created in the first place

When the bridge is disabled from the tray, `/health` and `/action/...` return `503` so widgets can treat it as offline.

## Endpoint

- `GET /health`
- `GET /network/status`
- `POST /action/lock`
- `POST /action/sleep`
- `POST /action/restart`
- `POST /action/shutdown`
- `POST /action/task-manager`
- `POST /action/power-settings`
- `POST /action/wifi-toggle`

Optional body:

```json
{
  "source": "power-pad",
  "label": "Lock"
}
```

## Widgets Using This Bridge

- `PowerPad/power_pad.html`
- `NetPulse/net_pulse.html`

Each widget can keep its own HTML and README while sharing the same local bridge process.

The tray currently lists:

- `Power Pad`
- `Net Pulse`

## Extending

For other widgets, add new actions in `main.go` or introduce dedicated handlers if you want to separate functional domains.

When adding a new widget:

- keep the widget HTML and widget-specific README in that widget's own folder
- point the widget to `http://127.0.0.1:39291`
- reuse existing actions when possible
- add new bridge actions only when the widget needs new Windows behavior
