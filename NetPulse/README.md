# Net Pulse

`net_pulse.html` is the source file for the Net Pulse widget.

## Using It In Corsair iCUE Xeneon Edge

1. Open [NetPulse/net_pulse.html](c:/Users/filip/Programming/XeneonWidgets/NetPulse/net_pulse.html) in a text editor.
2. Copy the full file contents.
3. Open the iframe widget inside the Corsair iCUE app for Xeneon Edge.
4. Paste the copied HTML into the widget content field.
5. Make sure the local WidgetBridge is running on `http://127.0.0.1:39291`.

## What It Shows

- live download throughput
- live upload throughput
- ping to the bridge target, default `1.1.1.1`
- primary network adapter detected by WidgetBridge

## Notes

- The widget talks to the shared local WidgetBridge service.
- If the bridge is offline, the footer status will show the connection error.
- You can change the ping target by starting WidgetBridge with `WIDGET_BRIDGE_PING_TARGET`.