# Bluetooth RadonEye RD200 Client

The RadonEye RD200 is tightly coupled with the mobile app and this is basically useless for home automation.  This is a very basic app to read the value using the [tinygo bluetooth](https://github.com/tinygo-org/bluetooth#go-bluetooth) library and publishes the radon value to an MQTT broker.

## Usage

```bash
Usage of ./radoneyebt:
  -L string set the log level (debug, info, warn, error) (default "info")
  -b string MQTT broker endpoint (default "tcp://iot.eclipse.org:1883")
  -m string The MQTT topic root (default "airmeter/radon")
  -r string RX Level Characteristic UUID (default "00001525-1212-efde-1523-785feabcd123")
  -s string Service UUID (default "00001523-1212-efde-1523-785feabcd123")
  -t string TX Level Characteristic UUID (default "00001523-1212-efde-1523-785feabcd123")
```

## Author

E Camden Fisher
