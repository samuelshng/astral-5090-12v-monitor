package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/jan-provaznik/sus"
)

func main() {
	ret := nvml.Init()
	if ret != nvml.SUCCESS {
		fmt.Fprintf(os.Stderr, "nvmlInit failed: %v\n", ret)
		os.Exit(1)
	}
	defer nvml.Shutdown()

	devices, err := sus.FindAstralDevices()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Could not detect compatible devices: %v\n", err)
		os.Exit(1)
	}
	if len(devices) == 0 {
		fmt.Fprintln(os.Stderr, "Could not find any compatible ASUS Astral RTX 5090 devices.")
		os.Exit(1)
	}

	fmt.Printf("Detected %d compatible ASUS Astral RTX 5090 device%s.\n", len(devices), plural(len(devices)))

	hadError := false
	for ordinal, device := range devices {
		if ordinal > 0 {
			fmt.Println()
		}
		printDeviceHeader(ordinal, device)

		snapshot, err := sus.ReadAstralDeviceSnapshot(device)
		if err != nil {
			hadError = true
			fmt.Fprintf(os.Stderr, "Read failed for %s: %v\n", device.Identifier(), err)
			if isPermissionError(err) {
				fmt.Fprintf(os.Stderr, "Permission hint: could not access %s; try sudo or install a host-specific udev rule for this i2c device.\n", device.I2CDevicePath())
			}
			continue
		}

		printSnapshot(snapshot)
	}

	if hadError {
		os.Exit(1)
	}
}

func printDeviceHeader(ordinal int, device sus.AstralDevice) {
	fmt.Printf("Device %d: %s\n", ordinal, device.Identifier())
	fmt.Printf("NVML index: %d\n", device.Index())
	fmt.Printf("PCI sysfs path: %s\n", device.PCISysfsPath())
	fmt.Printf("i2c device: %s\n", device.I2CDevicePath())
}

func printSnapshot(snapshot sus.AstralDeviceSnapshot) {
	fmt.Printf("Total NVML GPU load: %.1f W\n", snapshot.LoadWatts)
	fmt.Printf("Total connector draw: %.1f W\n", snapshot.Summary.TotalWatts)
	fmt.Printf("Pin draw min/max: %.1f W / %.1f W\n", snapshot.Summary.MinWatts, snapshot.Summary.MaxWatts)
	fmt.Printf("Balance ratio: %.2f\n", snapshot.Summary.BalanceRatio)
	for index, pin := range snapshot.Pins {
		fmt.Printf("Pin %d: %.3f V %.3f A %.1f W\n", index+1, pin.Voltage(), pin.Current(), pin.Drawing())
	}
}

func plural(count int) string {
	if count == 1 {
		return ""
	}
	return "s"
}

func isPermissionError(err error) bool {
	if errors.Is(err, os.ErrPermission) {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "permission denied")
}
