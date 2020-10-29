package main

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"
	"tinygo.org/x/bluetooth"
)

var (
	serviceUUIDString               = "00001523-1212-efde-1523-785feabcd123"
	txlevelCharacteristicUUIDString = "00001524-1212-efde-1523-785feabcd123"
	rxlevelCharacteristicUUIDString = "00001525-1212-efde-1523-785feabcd123"
)

type RadonEye struct {
	adapter                 *bluetooth.Adapter
	device                  *bluetooth.Device
	svcUUID, txUUID, rxUUID bluetooth.UUID
	scanResult              bluetooth.ScanResult
	levelService            bluetooth.DeviceService
	txChar, rxChar          bluetooth.DeviceCharacteristic
}

func main() {
	radonEye, err := NewRadonEye(bluetooth.DefaultAdapter)
	if err != nil {
		fmt.Printf("failed to create new radon eye: %s\n", err)
		os.Exit(2)
	}

	if err := radonEye.ScanForDevice(); err != nil {
		fmt.Printf("failed to scan for RadonEye: %s\n", err)
		os.Exit(3)
	}

	if err := radonEye.Connect(); err != nil {
		fmt.Printf("failed to connect to RadonEye: %s\n", err)
		os.Exit(4)
	}

	if err := radonEye.Services(); err != nil {
		fmt.Printf("failed to discover RadonEye services: %s\n", err)
		os.Exit(5)
	}

	if err := radonEye.Characteristics(); err != nil {
		fmt.Printf("failed to discover RadonEye level characteristics: %s\n", err)
		os.Exit(6)
	}

	if err := radonEye.HandleNotifications(); err != nil {
		fmt.Printf("failed to enable notification handler: %s\n", err)
		os.Exit(7)
	}

	ticker := time.NewTicker(20 * time.Second)
	termChan := make(chan os.Signal, 1)
	signal.Notify(termChan, syscall.SIGINT, syscall.SIGTERM)

	for {
		select {
		case <-ticker.C:
			if _, err = radonEye.txChar.WriteWithoutResponse([]byte{0x50}); err != nil {
				fmt.Printf("failed to enable TX notifications: %s\n", err)
				os.Exit(8)
			}
		case <-termChan:
			fmt.Println("Exiting...")
			ticker.Stop()
			if err := radonEye.Cleanup(); err != nil {
				fmt.Printf("failed to cleanup")
				os.Exit(10)
			}
			os.Exit(0)

		}
	}
}

func NewRadonEye(adapter *bluetooth.Adapter) (*RadonEye, error) {
	// Enable BLE interface.
	err := adapter.Enable()
	if err != nil {
		fmt.Printf("could not enable the BLE stack: %s\n", err)
		return nil, err
	}

	su, err := uuid.Parse(serviceUUIDString)
	if err != nil {
		fmt.Printf("couldn not parse service uuid: %s\n", err)
		return nil, err
	}
	serviceUUID := bluetooth.NewUUID(su)

	tlu, err := uuid.Parse(txlevelCharacteristicUUIDString)
	if err != nil {
		fmt.Printf("couldn not parse level uuid: %s\n", err)
		return nil, err
	}

	txLevelUUID := bluetooth.NewUUID(tlu)

	rlu, err := uuid.Parse(rxlevelCharacteristicUUIDString)
	if err != nil {
		fmt.Printf("couldn not parse level uuid: %s\n", err)
		return nil, err
	}

	rxLevelUUID := bluetooth.NewUUID(rlu)

	return &RadonEye{
		adapter: adapter,
		svcUUID: serviceUUID,
		txUUID:  txLevelUUID,
		rxUUID:  rxLevelUUID,
	}, nil
}

func (r *RadonEye) ScanForDevice() error {
	fmt.Println("Scanning for RadonEye...")
	if err := r.adapter.Scan(func(adapter *bluetooth.Adapter, result bluetooth.ScanResult) {
		if !result.AdvertisementPayload.HasServiceUUID(r.svcUUID) {
			return
		}
		r.scanResult = result

		// Stop the scan.
		err := r.adapter.StopScan()
		if err != nil {
			// Unlikely, but we can't recover from this.
			fmt.Printf("failed to stop the scan: %s\n", err)
		}
	}); err != nil {
		fmt.Printf("could not start a scan: %s\n", err)
		return err
	}

	return nil
}
func (r *RadonEye) Connect() error {
	fmt.Printf("Connecting to %s %s...\n", r.scanResult.Address.String(), r.scanResult.LocalName())

	r.adapter.SetConnectHandler(func(device bluetooth.Addresser, connected bool) {
		if connected {
			fmt.Printf("connected to device '%s'\n", r.scanResult.Address.String())
		} else {
			fmt.Print("not connected\n")
		}
	})

	device, err := r.adapter.Connect(r.scanResult.Address, bluetooth.ConnectionParams{})
	if err != nil {
		fmt.Printf("failed to connect to RadonEye: %s\n", err)
		return err
	}

	r.device = device

	return nil
}

func (r *RadonEye) Services() error {
	fmt.Println("discovering service...")
	services, err := r.device.DiscoverServices([]bluetooth.UUID{r.svcUUID})
	if err != nil {
		fmt.Printf("failed to discover the service: %s\n", err)
		return err
	}
	r.levelService = services[0]

	return nil
}

func (r *RadonEye) Characteristics() error {
	// Get the two characteristics present in this service.
	chars, err := r.levelService.DiscoverCharacteristics([]bluetooth.UUID{r.rxUUID, r.txUUID})
	if err != nil {
		fmt.Printf("failed to discover level characteristics: %s\n", err)
		return err
	}
	r.rxChar = chars[0]
	r.txChar = chars[1]

	return nil
}

func (r *RadonEye) HandleNotifications() error {
	// Enable notifications to receive incoming data.
	if err := r.rxChar.EnableNotifications(func(value []byte) {
		res := value[2:6]
		a := binary.LittleEndian.Uint32(res)
		a2 := math.Float32frombits(a)
		fmt.Printf("%.2f\n", a2)
	}); err != nil {
		fmt.Printf("failed to enable tx notifications: %s\n", err)
		return err
	}

	return nil
}

func (r *RadonEye) Cleanup() error {
	fmt.Printf("cleaning up...")
	return nil
}
