package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"flag"
	"math"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"
	"tinygo.org/x/bluetooth"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	log "github.com/sirupsen/logrus"
)

var (
	mqttBroker = flag.String("b", "tcp://iot.eclipse.org:1883", "MQTT broker endpoint")
	mqttTopic  = flag.String("m", "airmeter/radon", "The MQTT topic root")

	logLevel = flag.String("L", "info", "set the log level (debug, info, warn, error)")

	serviceUUIDString               = flag.String("s", "00001523-1212-efde-1523-785feabcd123", "Service UUID")
	txlevelCharacteristicUUIDString = flag.String("t", "00001524-1212-efde-1523-785feabcd123", "TX Level Characteristic UUID")
	rxlevelCharacteristicUUIDString = flag.String("r", "00001525-1212-efde-1523-785feabcd123", "RX Level Characteristic UUID")
)

type Reporter interface {
	Report(value float32) error
}

type LogReporter struct{}

func (r *LogReporter) Report(value float32) error {
	log.Infof("value: %.2f", value)
	return nil
}

type MqttReporter struct {
	mqttClient mqtt.Client
}

func (r *MqttReporter) Report(value float32) error {
	log.Debugf("reporting value %.2f to mqtt broker client %+v", value, r.mqttClient)

	v := struct{ RadonValue float32 }{value}
	j, err := json.Marshal(v)
	if err != nil {
		return err
	}

	token := r.mqttClient.Publish(*mqttTopic, 0, false, j)
	if ok := token.Wait(); !ok {
		log.Warn("token wait returned false")
	}
	return nil
}

type RadonEye struct {
	adapter                 *bluetooth.Adapter
	device                  *bluetooth.Device
	svcUUID, txUUID, rxUUID bluetooth.UUID
	scanResult              bluetooth.ScanResult
	levelService            bluetooth.DeviceService
	txChar, rxChar          bluetooth.DeviceCharacteristic
}

func main() {
	flag.Parse()

	switch *logLevel {
	case "error":
		log.SetLevel(log.ErrorLevel)
	case "warn":
		log.SetLevel(log.WarnLevel)
	case "info":
		log.SetLevel(log.InfoLevel)
	case "debug":
		log.SetLevel(log.DebugLevel)
	default:
		log.SetLevel(log.InfoLevel)
	}

	log.Info("starting new radon eye collector")

	radonEye, err := NewRadonEye(bluetooth.DefaultAdapter)
	if err != nil {
		log.Errorf("failed to create new radon eye: %s", err)
		os.Exit(2)
	}

	if err := radonEye.ScanForDevice(); err != nil {
		log.Errorf("failed to scan for RadonEye: %s", err)
		os.Exit(3)
	}

	if err := radonEye.Connect(); err != nil {
		log.Errorf("failed to connect to RadonEye: %s", err)
		os.Exit(4)
	}

	if err := radonEye.Services(); err != nil {
		log.Errorf("failed to discover RadonEye services: %s", err)
		os.Exit(5)
	}

	if err := radonEye.Characteristics(); err != nil {
		log.Errorf("failed to discover RadonEye level characteristics: %s", err)
		os.Exit(6)
	}

	mqttClient, err := newMQTTClient(*mqttBroker)
	if err != nil {
		log.Errorf("failed to setup MQTT client: %s", err)
		os.Exit(7)
	}
	defer mqttClient.Disconnect(250)

	mqttReporter := &MqttReporter{
		mqttClient: mqttClient,
	}

	ctx, cancel := context.WithCancel(context.Background())
	ticker := time.NewTicker(60 * time.Second)
	go func() {
		for iter := 0; iter < 5; iter++ {
			log.Infof("collection iteration %d", iter)

			timer := time.NewTimer(time.Duration(iter*10) * time.Second)
			<-timer.C

			if iter > 0 {
				log.Info("trying to reconnect")
				if err := radonEye.Connect(); err != nil {
					log.Warnf("failed to connect to RadonEye: %s", err)
				}
			}

			if err := radonEye.HandleNotifications(mqttReporter, &LogReporter{}); err != nil {
				log.Errorf("failed to enable notification handler: %s", err)
				continue
			}

			if err := radonEye.RunCollection(ctx, ticker, &iter); err != nil {
				log.Errorf("failed to run collection: %s", err)
				continue
			}

			return
		}

		ticker.Stop()
		cancel()
	}()

	termChan := make(chan os.Signal, 1)
	signal.Notify(termChan, syscall.SIGINT, syscall.SIGTERM)

	select {
	case <-termChan:
		log.Info("terminating on signal")
		cancel()
	case <-ctx.Done():
		log.Infof("context closed")
	}

	if err := radonEye.Cleanup(); err != nil {
		log.Errorf("failed to cleanup")
		os.Exit(11)
	}

	os.Exit(0)
}

func (r *RadonEye) RunCollection(ctx context.Context, ticker *time.Ticker, iter *int) error {
	for {
		select {
		case <-ticker.C:
			if _, err := r.txChar.WriteWithoutResponse([]byte{0x50}); err != nil {
				log.Warnf("failed to enable TX notifications: %s", err)
				return err
			}
			*iter = 0
		case <-ctx.Done():
			log.Infof("context done, exiting...")
			return nil
		}
	}

}

func NewRadonEye(adapter *bluetooth.Adapter) (*RadonEye, error) {
	// Enable BLE interface.
	err := adapter.Enable()
	if err != nil {
		log.Errorf("could not enable the BLE stack: %s", err)
		return nil, err
	}

	su, err := uuid.Parse(*serviceUUIDString)
	if err != nil {
		log.Errorf("couldn not parse service uuid: %s", err)
		return nil, err
	}
	serviceUUID := bluetooth.NewUUID(su)

	tlu, err := uuid.Parse(*txlevelCharacteristicUUIDString)
	if err != nil {
		log.Errorf("couldn not parse level uuid: %s", err)
		return nil, err
	}

	txLevelUUID := bluetooth.NewUUID(tlu)

	rlu, err := uuid.Parse(*rxlevelCharacteristicUUIDString)
	if err != nil {
		log.Errorf("couldn not parse level uuid: %s", err)
		return nil, err
	}

	rxLevelUUID := bluetooth.NewUUID(rlu)

	radoneye := RadonEye{
		adapter: adapter,
		svcUUID: serviceUUID,
		txUUID:  txLevelUUID,
		rxUUID:  rxLevelUUID,
	}

	log.Debugf("returning new radoneye: %+v", radoneye)

	return &radoneye, nil
}

func (r *RadonEye) ScanForDevice() error {
	log.Info("Scanning for RadonEye...")

	if err := r.adapter.Scan(func(adapter *bluetooth.Adapter, result bluetooth.ScanResult) {
		if !result.AdvertisementPayload.HasServiceUUID(r.svcUUID) {
			log.Warnf("advertisenment payload doesn't have service uuid %s", r.svcUUID.String())
			return
		}

		log.Debugf("service uuid %s found in advertisement payload", r.svcUUID.String())

		r.scanResult = result

		// Stop the scan.
		if err := r.adapter.StopScan(); err != nil {
			// Unlikely, but we can't recover from this.
			log.Warnf("failed to stop the scan: %s", err)
		}
	}); err != nil {
		log.Errorf("could not start a scan: %s", err)
		return err
	}

	return nil
}
func (r *RadonEye) Connect() error {
	log.Infof("Connecting to %s %s...", r.scanResult.Address.String(), r.scanResult.LocalName())

	r.adapter.SetConnectHandler(func(device bluetooth.Addresser, connected bool) {
		if connected {
			log.Infof("connected to device '%s'", r.scanResult.Address.String())
		} else {
			log.Infof("not connected\n")
		}
	})

	device, err := r.adapter.Connect(r.scanResult.Address, bluetooth.ConnectionParams{})
	if err != nil {
		log.Errorf("failed to connect to RadonEye: %s", err)
		return err
	}

	r.device = device

	return nil
}

func (r *RadonEye) Services() error {
	log.Info("discovering service...")
	services, err := r.device.DiscoverServices([]bluetooth.UUID{r.svcUUID})
	if err != nil {
		log.Errorf("failed to discover the service: %s", err)
		return err
	}
	r.levelService = services[0]

	return nil
}

func (r *RadonEye) Characteristics() error {
	// Get the two characteristics present in this service.
	chars, err := r.levelService.DiscoverCharacteristics([]bluetooth.UUID{r.rxUUID, r.txUUID})
	if err != nil {
		log.Errorf("failed to discover level characteristics: %s", err)
		return err
	}
	r.rxChar = chars[0]
	r.txChar = chars[1]

	return nil
}

func (r *RadonEye) HandleNotifications(reporters ...Reporter) error {
	// Enable notifications to receive incoming data.
	if err := r.rxChar.EnableNotifications(func(value []byte) {
		res := value[2:6]
		a := binary.LittleEndian.Uint32(res)
		a2 := math.Float32frombits(a)

		for _, r := range reporters {
			go func(r Reporter) {
				if err := r.Report(a2); err != nil {
					log.Warnf("error reporting: %s", err)
				}
			}(r)
		}

	}); err != nil {
		return err
	}

	return nil
}

func (r *RadonEye) Cleanup() error {
	log.Infof("cleaning up...")
	return nil
}

// newMQTTClient returns a new MQTT client with a random client ID and the broker endpoint
func newMQTTClient(broker string) (mqtt.Client, error) {
	clientID := uuid.New().String()

	opts := mqtt.NewClientOptions().AddBroker(broker)
	log.Infof("setting MQTT broker: %s", broker)

	opts.SetClientID(clientID)
	log.Infof("setting MQTT client ID: %s", clientID)

	// create and start a client using the above ClientOptions
	c := mqtt.NewClient(opts)
	if token := c.Connect(); token.Wait() && token.Error() != nil {
		return nil, token.Error()
	}

	return c, nil
}
