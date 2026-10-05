// 2026 Jan Provaznik (jan@provaznik.pro)
//

package sus

import "os"
import "math"
import "cmp"
import "fmt"
import "slices"
import "strings"
import "encoding/binary"

import "github.com/NVIDIA/go-nvml/pkg/nvml"
import "github.com/khirono/go-i2c/smbus"

// Constants
//

var nvidiaCompatibleDevice = []uint32{0x2b8510de}
var astralCompatibleDevice = []uint32{
	0x89e31043, // ROG-ASTRAL-RTX5090-O32G
	0x8a2e1043, // ROG-ASTRAL-RTX5090-O32G-WHITE
	0x8a5a1043, // ROG- STRIX-RTX5090-032G-BTF
}

const astralPinCount = 6

// Exported struct: AstralDevicePin
//
// .Voltage () (float64)
// .Current () (float64)
// .Drawing () (float64)

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

// Exported struct: AstralDevice
//
// .Identifier      ()               (string)
// .QueryDeviceLoad ()               (uint32, error)
// .QueryDevicePins ()               (uint32, error)
// .ScaleDeviceLoad (factor float64) (error)

type AstralDevice struct {
	index                  int
	sensorNumber           int
	deviceHandle           nvml.Device
	deviceDetailPci        nvml.PciInfo
	deviceDetailIdentifier string

	// ... constraints (power management, clock frequencies)
	devicePowerConstraintLower uint32
	devicePowerConstraintUpper uint32
	deviceClockMinimumGraphics uint32
	deviceClockMinimumMemories uint32
}

// Returns a readable identification of the device
func (self AstralDevice) Identifier() string {
	return self.deviceDetailIdentifier
}

// Queries the current power draw of the device, returns value in mW
func (self AstralDevice) QueryDeviceLoad() (uint32, error) {
	value, ret := nvml.DeviceGetPowerUsage(self.deviceHandle)
	if ret != nvml.SUCCESS {
		return 0, fmt.Errorf("nvmlDeviceGetPowerUsage failed (%w)", ret)
	}
	return value, nil
}

// Scales the current power draw of the device by 0 < scale < 1
func (self AstralDevice) ScaleDeviceLoad(scale float64) error {
	if math.IsNaN(scale) || math.IsInf(scale, 0) || scale <= 0 || scale >= 1 {
		return fmt.Errorf("invalid scale: must be finite and 0 < scale < 1")
	}

	current, err := self.QueryDeviceLoad()
	if err != nil {
		return err
	}

	lower, upper, ret := nvml.DeviceGetPowerManagementLimitConstraints(self.deviceHandle)
	if ret != nvml.SUCCESS {
		return fmt.Errorf("nvmlDeviceGetPowerManagementLimitConstraints failed (%w)", ret)
	}
	self.devicePowerConstraintLower, self.devicePowerConstraintUpper = lower, upper

	target := uint32(float64(current) * scale)

	// ... target below the configurable limit, declare emergency and throttle
	if target < self.devicePowerConstraintLower {
		return throttleDeviceClock(self)
	}

	return limitAstralDeviceLoad(self, target)
}

// Queries the current power draw of its pins, returns an array of readings
func (self AstralDevice) QueryDevicePins() ([]AstralDevicePin, error) {
	// Sensor address and register
	// ... via https://long-cat.net/gitea/moosecrap/evga-icx
	// ... via https://github.com/LibreHardwareMonitor/LibreHardwareMonitor
	// Sensor interaction (smbus)
	// ... via https://github.com/Timic3/astral-power-monitoring

	bus, err := smbus.Open(self.sensorNumber)
	if err != nil {
		return nil, fmt.Errorf("Could not open sensor (%d) device (%w)", self.sensorNumber, err)
	}
	defer bus.Close()

	if err := bus.SetSlaveAddr(0x2B, false); err != nil {
		return nil, fmt.Errorf("Could not pick sensor (%d, 0x2B) device (%w)", self.sensorNumber, err)
	}

	buffer := make([]byte, 24)
	length, err := bus.ReadI2CBlockData(0x80, buffer)
	if err != nil {
		return nil, fmt.Errorf("Could not read sensor (%d, 0x2B, 0x80) device (%w)", self.sensorNumber, err)
	}
	if length != 24 {
		return nil, fmt.Errorf("Could not read sensor (%d, 0x2B, 0x80) device, content too short", self.sensorNumber)
	}

	result := make([]AstralDevicePin, 6)
	for index := range 6 {
		start := 4 * index
		value := parseRegisterBuffer(buffer[start : start+4])
		result[index] = value
	}

	return result, nil
}

// Exported functions (with backwards compatibility)
//

func FindAstralDevices() ([]AstralDevice, error) {
	return findAstralDevices()
}
func ReadAstralDevicePins(target AstralDevice) ([]AstralDevicePin, error) {
	return target.QueryDevicePins()
}
func ReadAstralDeviceLoad(target AstralDevice) (float64, error) {
	value, err := target.QueryDeviceLoad()
	if err != nil {
		return 0, err
	}
	return float64(value) / 1000.0, nil
}
func LimitAstralDevice(device AstralDevice, scale float64) error {
	return device.ScaleDeviceLoad(scale)
}

// Implementation details
//

// Sets the clocks to their minimal values to prevent a catastrophic meltdown.
func throttleDeviceClock(self AstralDevice) error {
	graphics, memories, err := readClockConstraints(self.deviceHandle)
	if err != nil {
		return err
	}
	self.deviceClockMinimumGraphics, self.deviceClockMinimumMemories = graphics, memories
	var ret nvml.Return

	ret = nvml.DeviceSetGpuLockedClocks(self.deviceHandle, 0, self.deviceClockMinimumGraphics)
	if ret != nvml.SUCCESS {
		return fmt.Errorf("nvmlDeviceSetGpuLockedClocks failed (%w)", ret)
	}

	ret = nvml.DeviceSetMemoryLockedClocks(self.deviceHandle, 0, self.deviceClockMinimumMemories)
	if ret != nvml.SUCCESS {
		return fmt.Errorf("nvmlDeviceSetMemoryLockedClocks failed (%w)", ret)
	}

	return nil
}

// Sets the power limit using nvmlDeviceSetPowerManagementLimit procedure
func limitAstralDeviceLoad(self AstralDevice, target uint32) error {
	if target < self.devicePowerConstraintLower {
		return fmt.Errorf("target < devicePowerConstraintLower")
	}
	if target > self.devicePowerConstraintUpper {
		return fmt.Errorf("target > devicePowerConstraintUpper")
	}

	ret := nvml.DeviceSetPowerManagementLimit(self.deviceHandle, target)
	if ret != nvml.SUCCESS {
		return fmt.Errorf("nvmlDeviceSetPowerManagementLimit failed")
	}

	return nil
}

// Supporting functions
//

func parseRegisterBuffer(buffer []byte) AstralDevicePin {
	// Voltage (mV)
	wordOne := binary.BigEndian.Uint16(buffer[0:2])

	// Current (mA)
	wordTwo := binary.BigEndian.Uint16(buffer[2:4])

	return AstralDevicePin{
		voltage: float64(wordOne) / 1000,
		current: float64(wordTwo) / 1000,
	}
}

func findAstralDeviceSensorNumber(info nvml.PciInfo) (int, error) {
	root := fmt.Sprintf("/sys/bus/pci/devices/%04x:%02x:%02x.0",
		info.Domain, info.Bus, info.Device)

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
		return final, fmt.Errorf("Could not find sensor device")
	}

	return final, nil
}

func findAstralDevices() ([]AstralDevice, error) {
	var found []AstralDevice

	count, ret := nvml.DeviceGetCount()
	if ret != nvml.SUCCESS {
		return nil, fmt.Errorf("nvmlDeviceGetCount failed (%w)", ret)
	}

	for index := range count {
		device, ret := nvml.DeviceGetHandleByIndex(index)
		if ret != nvml.SUCCESS {
			return nil, fmt.Errorf("nvmlDeviceGetHandleByIndex failed (%w)", ret)
		}

		info, ret := nvml.DeviceGetPciInfo(device)
		if ret != nvml.SUCCESS {
			return nil, fmt.Errorf("nvmlDeviceGetPciInfo failed (%w)", ret)
		}

		if !slices.Contains(nvidiaCompatibleDevice, info.PciDeviceId) {
			continue
		}
		if !slices.Contains(astralCompatibleDevice, info.PciSubSystemId) {
			continue
		}

		uuid, ret := nvml.DeviceGetUUID(device)
		if ret != nvml.SUCCESS {
			return nil, fmt.Errorf("nvmlDeviceGetUUID failed (%w)", ret)
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

func readClockConstraints(device nvml.Device) (uint32, uint32, error) {
	// Determine the lowest supported graphics and memory clock frequencies by
	// examining all the available performance states.
	//
	// We can not use
	// ... nvmlDeviceGetSupportedMemoryClocks
	// ... nvmlDeviceGetSupportedGraphicsClocks
	// because go-nvml (v0.13.3) implements the C interface incorrectly.

	var leastFrequencyGraphics uint32 = 0xffffff
	var leastFrequencyMemories uint32 = 0xffffff

	// nvmlDeviceGetSupportedPerformanceStates
	var supported []nvml.Pstates

	supported, ret := nvml.DeviceGetSupportedPerformanceStates(device)
	if ret != nvml.SUCCESS {
		return 0, 0, fmt.Errorf("nvmlDeviceGetSupportedPerformanceStates failed (%w)", ret)
	}

	for _, pstate := range supported {
		valueGraphics, valueMemories, err := readClockConstraintsOfPerformanceState(device, pstate)
		if err != nil {
			return 0, 0, err
		}

		if valueGraphics < leastFrequencyGraphics {
			leastFrequencyGraphics = valueGraphics
		}
		if valueMemories < leastFrequencyMemories {
			leastFrequencyMemories = valueMemories
		}
	}

	return leastFrequencyGraphics, leastFrequencyMemories, nil
}

func readClockConstraintsOfPerformanceState(device nvml.Device, pstate nvml.Pstates) (uint32, uint32, error) {
	var valueGraphics, valueMemories uint32

	valueGraphics, _, ret := nvml.DeviceGetMinMaxClockOfPState(device, nvml.CLOCK_GRAPHICS, pstate)
	if ret != nvml.SUCCESS {
		return 0, 0, fmt.Errorf("nvmlDeviceGetMinMaxClockOfPState (0) failed (%w)", ret)
	}

	valueMemories, _, ret = nvml.DeviceGetMinMaxClockOfPState(device, nvml.CLOCK_MEM, pstate)
	if ret != nvml.SUCCESS {
		return 0, 0, fmt.Errorf("nvmlDeviceGetMinMaxClockOfPState (2) failed (%w)", ret)
	}

	return valueGraphics, valueMemories, nil
}

// Fork telemetry and compatibility helpers.
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

func readBuffer(buffer []byte) AstralDevicePin { return parseRegisterBuffer(buffer) }
