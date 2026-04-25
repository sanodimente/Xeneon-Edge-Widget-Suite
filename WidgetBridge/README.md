# WidgetBridge

A shared local Go server that exposes Windows actions to multiple web widgets loaded in Xeneon Edge.

## Run

```powershell
cd C:\Users\filip\Programming\XeneonWidgets\WidgetBridge
go run .
```

Or use the prebuilt binary:

```powershell
cd C:\Users\filip\Programming\XeneonWidgets\WidgetBridge
.\widgetbridge.exe
```

To stop the currently running instance:

```powershell
cd C:\Users\filip\Programming\XeneonWidgets\WidgetBridge
.\widgetbridge.exe --stop
```

The server listens by default on `http://127.0.0.1:39291`.

Any widget that knows this local endpoint can reuse the same bridge.

## Endpoint

- `GET /health`
- `POST /action/lock`
- `POST /action/sleep`
- `POST /action/restart`
- `POST /action/shutdown`
- `POST /action/task-manager`
- `POST /action/power-settings`

Optional body:

```json
{
  "source": "power-pad",
  "label": "Lock"
}
```

## Widgets Using This Bridge

- `PowerPad/power_pad.html`

Each widget can keep its own HTML and README while sharing the same local bridge process.

## Extending

For other widgets, add new actions in `main.go` or introduce dedicated handlers if you want to separate functional domains.

When adding a new widget:

- keep the widget HTML and widget-specific README in that widget's own folder
- point the widget to `http://127.0.0.1:39291`
- reuse existing actions when possible
- add new bridge actions only when the widget needs new Windows behavior
