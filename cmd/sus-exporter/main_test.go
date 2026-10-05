package main

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type fakeReader struct {
	results []exporterSnapshotResult
	err     error
}

func (reader fakeReader) ReadAll() ([]exporterSnapshotResult, error) {
	return reader.results, reader.err
}

func TestCollectorEmitsSnapshotMetrics(t *testing.T) {
	snapshot, err := newExporterSnapshot("GPU-test", 2, 321.4, []exporterPin{
		{Voltage: 12, Current: 1, Power: 12},
		{Voltage: 12, Current: 2, Power: 24},
		{Voltage: 12, Current: 3, Power: 36},
		{Voltage: 12, Current: 4, Power: 48},
		{Voltage: 12, Current: 5, Power: 60},
		{Voltage: 12, Current: 6, Power: 72},
	})
	if err != nil {
		t.Fatalf("newExporterSnapshot returned error: %v", err)
	}

	body := gatherMetrics(t, fakeReader{results: []exporterSnapshotResult{{Device: snapshot.Device, Snapshot: snapshot}}})

	for _, want := range []string{
		`sus_pin_voltage_volts{gpu_index="2",gpu_uuid="GPU-test",pin="1"} 12`,
		`sus_pin_current_amps{gpu_index="2",gpu_uuid="GPU-test",pin="6"} 6`,
		`sus_pin_power_watts{gpu_index="2",gpu_uuid="GPU-test",pin="6"} 72`,
		`sus_connector_power_watts{gpu_index="2",gpu_uuid="GPU-test"} 252`,
		`sus_gpu_nvml_power_watts{gpu_index="2",gpu_uuid="GPU-test"} 321.4`,
		`sus_pin_balance_ratio{gpu_index="2",gpu_uuid="GPU-test"} 0.16666666666666666`,
		`sus_device_read_success{gpu_index="2",gpu_uuid="GPU-test"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics body missing %q:\n%s", want, body)
		}
	}
}

func TestCollectorEmitsPerDeviceFailure(t *testing.T) {
	body := gatherMetrics(t, fakeReader{results: []exporterSnapshotResult{{
		Device: exporterDevice{UUID: "GPU-test", Index: 0},
		Err:    errors.New("read pins: permission denied"),
	}}})

	for _, want := range []string{
		`sus_device_read_success{gpu_index="0",gpu_uuid="GPU-test"} 0`,
		`sus_scrape_errors_total{reason="read_pins"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics body missing %q:\n%s", want, body)
		}
	}
}

func TestCollectorHandlesConcurrentScrapes(t *testing.T) {
	snapshot, err := newExporterSnapshot("GPU-test", 0, 10, []exporterPin{
		{Voltage: 12, Current: 1, Power: 12},
		{Voltage: 12, Current: 1, Power: 12},
		{Voltage: 12, Current: 1, Power: 12},
		{Voltage: 12, Current: 1, Power: 12},
		{Voltage: 12, Current: 1, Power: 12},
		{Voltage: 12, Current: 1, Power: 12},
	})
	if err != nil {
		t.Fatalf("newExporterSnapshot returned error: %v", err)
	}

	registry := prometheus.NewRegistry()
	registry.MustRegister(newSusCollector(fakeReader{results: []exporterSnapshotResult{{Device: snapshot.Device, Snapshot: snapshot}}}, log.New(io.Discard, "", 0)))
	handler := promhttp.HandlerFor(registry, promhttp.HandlerOpts{})

	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			request := httptest.NewRequest("GET", "/metrics", nil)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != 200 {
				t.Errorf("response code = %d, want 200", response.Code)
			}
		}()
	}
	wg.Wait()
}

func gatherMetrics(t *testing.T, reader telemetryReader) string {
	t.Helper()

	registry := prometheus.NewRegistry()
	registry.MustRegister(newSusCollector(reader, log.New(io.Discard, "", 0)))
	handler := promhttp.HandlerFor(registry, promhttp.HandlerOpts{})

	request := httptest.NewRequest("GET", "/metrics", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 200 {
		t.Fatalf("response code = %d, want 200", response.Code)
	}
	return response.Body.String()
}

func newExporterSnapshot(uuid string, index int, loadWatts float64, pins []exporterPin) (exporterSnapshot, error) {
	if len(pins) != 6 {
		return exporterSnapshot{}, fmt.Errorf("expected 6 pins, got %d", len(pins))
	}

	summary := exporterSummary{MinWatts: pins[0].Power, MaxWatts: pins[0].Power}
	for _, pin := range pins {
		summary.TotalWatts += pin.Power
		if pin.Power < summary.MinWatts {
			summary.MinWatts = pin.Power
		}
		if pin.Power > summary.MaxWatts {
			summary.MaxWatts = pin.Power
		}
	}
	if summary.MaxWatts <= 0 {
		return exporterSnapshot{}, fmt.Errorf("maximum pin draw is zero")
	}
	summary.BalanceRatio = summary.MinWatts / summary.MaxWatts

	return exporterSnapshot{
		Device:    exporterDevice{UUID: uuid, Index: index},
		LoadWatts: loadWatts,
		Pins:      pins,
		Summary:   summary,
	}, nil
}
