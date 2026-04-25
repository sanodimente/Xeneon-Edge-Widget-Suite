# Power Pad

`power_pad.html` is the source file for the Power Pad widget.

## Using It In Corsair iCUE Xeneon Edge

1. Open [power_pad.html](c:/Users/filip/Programming/XeneonWidgets/PowerPad/power_pad.html) in a text editor.
2. Copy the full file contents.
3. Open the iframe widget inside the Corsair iCUE app for Xeneon Edge.
4. Paste the copied HTML into the widget content field.
5. Make sure the local WidgetBridge is running on `http://127.0.0.1:39291`.

## Notes

- The widget talks to the shared local WidgetBridge service.
- If the bridge is offline, the footer status will show the connection error.
- This folder can contain other Power Pad variants without changing the bridge itself.