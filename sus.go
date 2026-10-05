// 2026 Jan Provaznik (jan@provaznik.pro)
//

package sus

import (
	"cmp"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"slices"
	"strings"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/khirono/go-i2c/smbus"
)

// Constants
//

var nvidiaCompatibleDevice = []uint32{0x2b8510de}
var astralCompatibleDevice = []uint32{
	0x89e31043, // ROG-ASTRAL-RTX5090-O32G
	0x8a2e1043, // ROG-ASTRAL-RTX5090-O32G-WHITE
}

const astralPinCount = 6

// Exported types and methods
//

type AstralDevice struct {
	index                  int
	sensorNumber           int
	deviceHandle           nvml.Device
	deviceDetailPci        nvml.PciInfo
	deviceDetailIdentifier string
}

func (self AstralDevice) Identifier() string {
	return self.deviceDetailIdentifier
}

func (self AstralDevice) Index() int {
	return self.index
}

func (self AstralDevice) I2CBusNumber() int {
	return self.sensorNumber
}

func (self AstralDevice) I2CDevicePath() string {
	return fmt.Sprintf("/dev/i2c-%d", self.sensorNumber)
}

func (self AstralDevice) PCISysfsPath() string {
	return pciSysfsPath(self.deviceDetailPci)
}

type AstralDevicePin struct {
	voltage float64
	current float64
}

func (self AstralDevicePin) Voltage() float64 {
	return self.voltage
}

func (self AstralDevicePin) Current() float64 {
	return self.current
}

func (self AstralDevicePin) Drawing() float64 {
	return self.voltage * self.current
}

type AstralPinSummary struct {
	TotalWatts   float64
	MinWatts     float64
	MaxWatts     float64
	BalanceRatio float64
}

type AstralDeviceSnapshot struct {
	Device    AstralDevice
	LoadWatts float64
	Pins      []AstralDevicePin
	Summary   AstralPinSummary
}

type AstralDeviceSnapshotResult struct {
	Device   AstralDevice
	Snapshot AstralDeviceSnapshot
	Err      error
}

// Exported functions
//

func FindAstralDevices() ([]AstralDevice, error) {
	var found []AstralDevice

	count, ret := nvml.DeviceGetCount()
	if ret != nvml.SUCCESS {
		return nil, fmt.Errorf("nvmlDeviceGetCount failed")
	}

	for index := range count {
		device, ret := nvml.DeviceGetHandleByIndex(index)
		if ret != nvml.SUCCESS {
			return nil, fmt.Errorf("nvmlDeviceGetHandleByIndex failed")
		}

		info, ret := nvml.DeviceGetPciInfo(device)
		if ret != nvml.SUCCESS {
			return nil, fmt.Errorf("nvmlDeviceGetPciInfo failed")
		}

		if !slices.Contains(nvidiaCompatibleDevice, info.PciDeviceId) {
			continue
		}
		if !slices.Contains(astralCompatibleDevice, info.PciSubSystemId) {
			continue
		}

		uuid, ret := nvml.DeviceGetUUID(device)
		if ret != nvml.SUCCESS {
			return nil, fmt.Errorf("nvmlDeviceGetUUID failed")
		}

		number, err := findAstralDeviceSensorNumber(info)
		if err != nil {
			return nil, err
		}

		current := AstralDevice{
			index:                  index,
			sensorNumber:           number,
			deviceHandle:           device,
			deviceDetailPci:        info,
			deviceDetailIdentifier: uuid,
		}

		found = append(found, current)
	}

	return found, nil
}

func ReadAstralDevicePins(target AstralDevice) ([]AstralDevicePin, error) {
	// Sensor address and register
	// ... via https://long-cat.net/gitea/moosecrap/evga-icx
	// ... via https://github.com/LibreHardwareMonitor/LibreHardwareMonitor
	//
	// Sensor interaction (smbus)
	// ... via https://github.com/Timic3/astral-power-monitoring

	bus, err := smbus.Open(target.sensorNumber)
	if err != nil {
		return nil, fmt.Errorf("open %s for %s: %w", target.I2CDevicePath(), target.PCISysfsPath(), err)
	}
	defer bus.Close()

	err = bus.SetSlaveAddr(0x2B, false)
	if err != nil {
		return nil, fmt.Errorf("set slave address on %s: %w", target.I2CDevicePath(), err)
	}

	buffer := make([]byte, 24)
	length, err := bus.ReadI2CBlockData(0x80, buffer)
	if err != nil {
		return nil, fmt.Errorf("read sensor block from %s: %w", target.I2CDevicePath(), err)
	}

	if length != 24 {
		return nil, fmt.Errorf("could not read sensor device from %s", target.I2CDevicePath())
	}

	result := make([]AstralDevicePin, astralPinCount)
	for index := range astralPinCount {
		start := 4 * index
		result[index] = readBuffer(buffer[start : start+4])
	}

	return result, nil
}

func ReadAstralDeviceLoad(target AstralDevice) (float64, error) {
	// nvmlDeviceGetPowerUsage f
	// ... deals in mW

	value, ret := nvml.DeviceGetPowerUsage(target.deviceHandle)
	if ret != nvml.SUCCESS {
		return -1, fmt.Errorf("nvmlDeviceGetPowerUsage failed")
	}
	return float64(value) / 1000, nil
}

func SummarizePins(pins []AstralDevicePin) (AstralPinSummary, error) {
	if len(pins) != astralPinCount {
		return AstralPinSummary{}, fmt.Errorf("expected %d pins, got %d", astralPinCount, len(pins))
	}

	summary := AstralPinSummary{
		MinWatts: math.Inf(1),
	}

	for index, pin := range pins {
		voltage := pin.Voltage()
		current := pin.Current()
		if !validFinite(voltage) {
			return AstralPinSummary{}, fmt.Errorf("pin %d has invalid voltage %v", index+1, voltage)
		}
		if !validFinite(current) {
			return AstralPinSummary{}, fmt.Errorf("pin %d has invalid current %v", index+1, current)
		}
		if voltage < 0 {
			return AstralPinSummary{}, fmt.Errorf("pin %d has negative voltage %v", index+1, voltage)
		}
		if current < 0 {
			return AstralPinSummary{}, fmt.Errorf("pin %d has negative current %v", index+1, current)
		}

		draw := pin.Drawing()
		if !validFinite(draw) {
			return AstralPinSummary{}, fmt.Errorf("pin %d has invalid draw %v", index+1, draw)
		}
		if draw > summary.MaxWatts {
			summary.MaxWatts = draw
		}
		if draw < summary.MinWatts {
			summary.MinWatts = draw
		}
		summary.TotalWatts += draw
	}

	if summary.MaxWatts <= 0 {
		return AstralPinSummary{}, fmt.Errorf("maximum pin draw is zero")
	}

	summary.BalanceRatio = summary.MinWatts / summary.MaxWatts
	return summary, nil
}

func ReadAstralDeviceSnapshot(target AstralDevice) (AstralDeviceSnapshot, error) {
	pins, err := ReadAstralDevicePins(target)
	if err != nil {
		return AstralDeviceSnapshot{}, fmt.Errorf("read pins for %s: %w", target.Identifier(), err)
	}

	summary, err := SummarizePins(pins)
	if err != nil {
		return AstralDeviceSnapshot{}, fmt.Errorf("summarize pins for %s: %w", target.Identifier(), err)
	}

	load, err := ReadAstralDeviceLoad(target)
	if err != nil {
		return AstralDeviceSnapshot{}, fmt.Errorf("read load for %s: %w", target.Identifier(), err)
	}

	return AstralDeviceSnapshot{
		Device:    target,
		LoadWatts: load,
		Pins:      pins,
		Summary:   summary,
	}, nil
}

func ReadAllAstralDeviceSnapshots() ([]AstralDeviceSnapshotResult, error) {
	devices, err := FindAstralDevices()
	if err != nil {
		return nil, err
	}

	results := make([]AstralDeviceSnapshotResult, 0, len(devices))
	for _, device := range devices {
		snapshot, err := ReadAstralDeviceSnapshot(device)
		results = append(results, AstralDeviceSnapshotResult{
			Device:   device,
			Snapshot: snapshot,
			Err:      err,
		})
	}

	return results, nil
}

// Emergency actions
//

func LimitAstralDeviceFreq(target AstralDevice) (uint32, error) {
	current, ret := nvml.DeviceGetClockInfo(target.deviceHandle, nvml.CLOCK_GRAPHICS)
	if ret != nvml.SUCCESS {
		return 0, fmt.Errorf("nvmlDeviceGetClockInfo failed")
	}

	value := int32(current)
	limit := uint32(clamp(value-500, 100, value))

	ret = nvml.DeviceSetGpuLockedClocks(target.deviceHandle, 0, limit)
	if ret != nvml.SUCCESS {
		return 0, fmt.Errorf("nvmlDeviceSetGpuLockedClocks failed")
	}

	return limit, nil
}

func LimitAstralDeviceLoad(target AstralDevice) (float64, error) {
	var watts float64

	// nvmlDeviceGetPowerManagementLimitConstraints
	// nvmlDeviceGetPowerManagementLimit
	// nvmlDeviceSetPowerManagementLimit
	// ... both deal in mW

	limitLower, limitUpper, ret := nvml.DeviceGetPowerManagementLimitConstraints(target.deviceHandle)
	if ret != nvml.SUCCESS {
		return watts, fmt.Errorf("nvmlDeviceGetPowerManagementLimitConstraints failed")
	}

	limitCurrent, ret := nvml.DeviceGetPowerManagementLimit(target.deviceHandle)
	if ret != nvml.SUCCESS {
		return watts, fmt.Errorf("nvmlDeviceGetPowerManagementLimit failed")
	}

	// ... power limit can be only set within the (lower, upper) range
	limit := clamp(limitCurrent-5000, limitLower, limitUpper)

	ret = nvml.DeviceSetPowerManagementLimit(target.deviceHandle, limit)
	if ret != nvml.SUCCESS {
		return watts, fmt.Errorf("nvmlDeviceSetPowerManagementLimit failed")
	}

	watts = float64(limit) / 1000
	return watts, nil
}

// Supporting functions
//

func readBuffer(buffer []byte) AstralDevicePin {
	wordOne := binary.BigEndian.Uint16(buffer[0:2])
	wordTwo := binary.BigEndian.Uint16(buffer[2:4])

	return AstralDevicePin{
		voltage: float64(wordOne) / 1000,
		current: float64(wordTwo) / 1000,
	}
}

func findAstralDeviceSensorNumber(info nvml.PciInfo) (int, error) {
	root := pciSysfsPath(info)

	final := 0xffff
	value := 0xffff

	entries, err := os.ReadDir(root)
	if err != nil {
		return 0xffff, err
	}

	for _, item := range entries {
		if !strings.HasPrefix(item.Name(), "i2c-") {
			continue
		}

		num, err := fmt.Sscanf(item.Name(), "i2c-%d", &value)
		if err != nil {
			return 0xffff, err
		}
		if num != 1 {
			continue
		}
		if value < final {
			final = value
		}
	}

	if final == 0xffff {
		return final, fmt.Errorf("could not find sensor device")
	}

	return final, nil
}

func pciSysfsPath(info nvml.PciInfo) string {
	return fmt.Sprintf("/sys/bus/pci/devices/%04x:%02x:%02x.0",
		info.Domain, info.Bus, info.Device)
}

func validFinite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func clamp[V cmp.Ordered](value V, lower V, upper V) V {
	if value > upper {
		return upper
	}
	if value < lower {
		return lower
	}
	return value
}
