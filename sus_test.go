package sus

import (
	"math"
	"testing"
)

func TestSummarizePins(t *testing.T) {
	pins := []AstralDevicePin{
		{voltage: 12, current: 1},
		{voltage: 12, current: 2},
		{voltage: 12, current: 3},
		{voltage: 12, current: 4},
		{voltage: 12, current: 5},
		{voltage: 12, current: 6},
	}

	summary, err := SummarizePins(pins)
	if err != nil {
		t.Fatalf("SummarizePins returned error: %v", err)
	}
	if summary.TotalWatts != 252 {
		t.Fatalf("TotalWatts = %v, want 252", summary.TotalWatts)
	}
	if summary.MinWatts != 12 {
		t.Fatalf("MinWatts = %v, want 12", summary.MinWatts)
	}
	if summary.MaxWatts != 72 {
		t.Fatalf("MaxWatts = %v, want 72", summary.MaxWatts)
	}
	if summary.BalanceRatio != float64(1)/6 {
		t.Fatalf("BalanceRatio = %v, want %v", summary.BalanceRatio, float64(1)/6)
	}
}

func TestSummarizePinsRejectsWrongPinCount(t *testing.T) {
	_, err := SummarizePins([]AstralDevicePin{{voltage: 12, current: 1}})
	if err == nil {
		t.Fatal("SummarizePins returned nil error for wrong pin count")
	}
}

func TestSummarizePinsRejectsZeroMaxDraw(t *testing.T) {
	pins := []AstralDevicePin{
		{voltage: 0, current: 0},
		{voltage: 0, current: 0},
		{voltage: 0, current: 0},
		{voltage: 0, current: 0},
		{voltage: 0, current: 0},
		{voltage: 0, current: 0},
	}

	_, err := SummarizePins(pins)
	if err == nil {
		t.Fatal("SummarizePins returned nil error for zero maximum draw")
	}
}

func TestSummarizePinsRejectsNegativeInput(t *testing.T) {
	pins := validTestPins()
	pins[2].current = -1

	_, err := SummarizePins(pins)
	if err == nil {
		t.Fatal("SummarizePins returned nil error for negative current")
	}
}

func TestSummarizePinsRejectsNonFiniteInput(t *testing.T) {
	pins := validTestPins()
	pins[4].voltage = math.NaN()

	_, err := SummarizePins(pins)
	if err == nil {
		t.Fatal("SummarizePins returned nil error for NaN voltage")
	}

	pins = validTestPins()
	pins[4].current = math.Inf(1)

	_, err = SummarizePins(pins)
	if err == nil {
		t.Fatal("SummarizePins returned nil error for infinite current")
	}
}

func validTestPins() []AstralDevicePin {
	return []AstralDevicePin{
		{voltage: 12, current: 1},
		{voltage: 12, current: 1},
		{voltage: 12, current: 1},
		{voltage: 12, current: 1},
		{voltage: 12, current: 1},
		{voltage: 12, current: 1},
	}
}

// Invalid reductions must fail before consulting or mutating GPU hardware.
func TestScaleDeviceLoadRejectsInvalidScaleBeforeHardwareAccess(t *testing.T) {
	device := AstralDevice{}
	for _, scale := range []float64{0, 1, -1, 2, math.NaN(), math.Inf(1), math.Inf(-1)} {
		if err := device.ScaleDeviceLoad(scale); err == nil {
			t.Fatalf("ScaleDeviceLoad(%v) accepted an invalid reduction", scale)
		}
	}
}
