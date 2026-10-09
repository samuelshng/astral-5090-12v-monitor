package main

import (
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/jan-provaznik/sus"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const namespace = "sus"

type telemetryReader interface {
	ReadAll() ([]exporterSnapshotResult, error)
}

type hardwareReader struct{}

type exporterSnapshotResult struct {
	Device   exporterDevice
	Snapshot exporterSnapshot
	Err      error
}

type exporterDevice struct {
	UUID  string
	Index int
}

type exporterSnapshot struct {
	Device    exporterDevice
	LoadWatts float64
	Pins      []exporterPin
	Summary   exporterSummary
}

type exporterPin struct {
	Voltage float64
	Current float64
	Power   float64
}

type exporterSummary struct {
	TotalWatts   float64
	MinWatts     float64
	MaxWatts     float64
	BalanceRatio float64
}

func (hardwareReader) ReadAll() ([]exporterSnapshotResult, error) {
	results, err := sus.ReadAllAstralDeviceSnapshots()
	if err != nil {
		return nil, err
	}

	exported := make([]exporterSnapshotResult, 0, len(results))
	for _, result := range results {
		device := exporterDevice{
			UUID:  result.Device.Identifier(),
			Index: result.Device.Index(),
		}
		exportedResult := exporterSnapshotResult{
			Device: device,
			Err:    result.Err,
		}
		if result.Err == nil {
			exportedResult.Snapshot = convertSnapshot(result.Snapshot)
		}
		exported = append(exported, exportedResult)
	}

	return exported, nil
}

func convertSnapshot(snapshot sus.AstralDeviceSnapshot) exporterSnapshot {
	pins := make([]exporterPin, 0, len(snapshot.Pins))
	for _, pin := range snapshot.Pins {
		pins = append(pins, exporterPin{
			Voltage: pin.Voltage(),
			Current: pin.Current(),
			Power:   pin.Drawing(),
		})
	}

	return exporterSnapshot{
		Device: exporterDevice{
			UUID:  snapshot.Device.Identifier(),
			Index: snapshot.Device.Index(),
		},
		LoadWatts: snapshot.LoadWatts,
		Pins:      pins,
		Summary: exporterSummary{
			TotalWatts:   snapshot.Summary.TotalWatts,
			MinWatts:     snapshot.Summary.MinWatts,
			MaxWatts:     snapshot.Summary.MaxWatts,
			BalanceRatio: snapshot.Summary.BalanceRatio,
		},
	}
}

type susCollector struct {
	reader telemetryReader
	logger *log.Logger

	pinVoltage        *prometheus.Desc
	pinCurrent        *prometheus.Desc
	pinPower          *prometheus.Desc
	connectorPower    *prometheus.Desc
	gpuPower          *prometheus.Desc
	pinMinPower       *prometheus.Desc
	pinMaxPower       *prometheus.Desc
	pinBalance        *prometheus.Desc
	deviceReadSuccess *prometheus.Desc
	scrapeErrors      *prometheus.Desc

	mu     sync.Mutex
	errors map[string]float64
}

func newSusCollector(reader telemetryReader, logger *log.Logger) *susCollector {
	if logger == nil {
		logger = log.New(os.Stderr, "sus-exporter: ", log.LstdFlags)
	}

	return &susCollector{
		reader: reader,
		logger: logger,
		pinVoltage: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "pin", "voltage_volts"),
			"Voltage measured at an ASUS Astral 12V-2x6 connector pin.",
			[]string{"gpu_uuid", "gpu_index", "pin"}, nil,
		),
		pinCurrent: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "pin", "current_amps"),
			"Current measured at an ASUS Astral 12V-2x6 connector pin.",
			[]string{"gpu_uuid", "gpu_index", "pin"}, nil,
		),
		pinPower: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "pin", "power_watts"),
			"Power calculated as voltage times current for one ASUS Astral 12V-2x6 connector pin.",
			[]string{"gpu_uuid", "gpu_index", "pin"}, nil,
		),
		connectorPower: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "connector", "power_watts"),
			"Sum of the six measured ASUS Astral 12V-2x6 connector pin powers.",
			[]string{"gpu_uuid", "gpu_index"}, nil,
		),
		gpuPower: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "gpu_nvml", "power_watts"),
			"GPU power reported by NVIDIA NVML.",
			[]string{"gpu_uuid", "gpu_index"}, nil,
		),
		pinMinPower: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "pin_min", "power_watts"),
			"Minimum power across the six ASUS Astral 12V-2x6 connector pins.",
			[]string{"gpu_uuid", "gpu_index"}, nil,
		),
		pinMaxPower: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "pin_max", "power_watts"),
			"Maximum power across the six ASUS Astral 12V-2x6 connector pins.",
			[]string{"gpu_uuid", "gpu_index"}, nil,
		),
		pinBalance: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "pin", "balance_ratio"),
			"Ratio of minimum pin power to maximum pin power.",
			[]string{"gpu_uuid", "gpu_index"}, nil,
		),
		deviceReadSuccess: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "device_read", "success"),
			"Whether reading this GPU succeeded during the scrape, 1 for success and 0 for failure.",
			[]string{"gpu_uuid", "gpu_index"}, nil,
		),
		scrapeErrors: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "scrape_errors", "total"),
			"Total scrape errors by stable reason.",
			[]string{"reason"}, nil,
		),
		errors: map[string]float64{
			"find_devices": 0,
			"read_pins":    0,
			"read_load":    0,
			"summarize":    0,
			"unknown":      0,
		},
	}
}

func (collector *susCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- collector.pinVoltage
	ch <- collector.pinCurrent
	ch <- collector.pinPower
	ch <- collector.connectorPower
	ch <- collector.gpuPower
	ch <- collector.pinMinPower
	ch <- collector.pinMaxPower
	ch <- collector.pinBalance
	ch <- collector.deviceReadSuccess
	ch <- collector.scrapeErrors
}

func (collector *susCollector) Collect(ch chan<- prometheus.Metric) {
	results, err := collector.reader.ReadAll()
	if err != nil {
		collector.logger.Printf("scrape failed while finding devices: %v", err)
		collector.incrementError("find_devices")
		collector.collectErrorCounters(ch)
		return
	}

	for _, result := range results {
		device := result.Device
		if result.Err != nil {
			reason := classifyError(result.Err)
			collector.logger.Printf("scrape failed for gpu_uuid=%s gpu_index=%d reason=%s: %v", device.UUID, device.Index, reason, result.Err)
			collector.incrementError(reason)
			ch <- prometheus.MustNewConstMetric(collector.deviceReadSuccess, prometheus.GaugeValue, 0, device.UUID, strconv.Itoa(device.Index))
			continue
		}

		snapshot := result.Snapshot
		if snapshot.Device.UUID == "" {
			snapshot.Device = device
		}
		collector.collectSnapshot(ch, snapshot)
	}

	collector.collectErrorCounters(ch)
}

func (collector *susCollector) collectSnapshot(ch chan<- prometheus.Metric, snapshot exporterSnapshot) {
	uuid := snapshot.Device.UUID
	index := strconv.Itoa(snapshot.Device.Index)

	ch <- prometheus.MustNewConstMetric(collector.deviceReadSuccess, prometheus.GaugeValue, 1, uuid, index)
	ch <- prometheus.MustNewConstMetric(collector.connectorPower, prometheus.GaugeValue, snapshot.Summary.TotalWatts, uuid, index)
	ch <- prometheus.MustNewConstMetric(collector.gpuPower, prometheus.GaugeValue, snapshot.LoadWatts, uuid, index)
	ch <- prometheus.MustNewConstMetric(collector.pinMinPower, prometheus.GaugeValue, snapshot.Summary.MinWatts, uuid, index)
	ch <- prometheus.MustNewConstMetric(collector.pinMaxPower, prometheus.GaugeValue, snapshot.Summary.MaxWatts, uuid, index)
	ch <- prometheus.MustNewConstMetric(collector.pinBalance, prometheus.GaugeValue, snapshot.Summary.BalanceRatio, uuid, index)

	for pinIndex, pin := range snapshot.Pins {
		pinLabel := strconv.Itoa(pinIndex + 1)
		ch <- prometheus.MustNewConstMetric(collector.pinVoltage, prometheus.GaugeValue, pin.Voltage, uuid, index, pinLabel)
		ch <- prometheus.MustNewConstMetric(collector.pinCurrent, prometheus.GaugeValue, pin.Current, uuid, index, pinLabel)
		ch <- prometheus.MustNewConstMetric(collector.pinPower, prometheus.GaugeValue, pin.Power, uuid, index, pinLabel)
	}
}

func (collector *susCollector) incrementError(reason string) {
	collector.mu.Lock()
	defer collector.mu.Unlock()

	if _, ok := collector.errors[reason]; !ok {
		reason = "unknown"
	}
	collector.errors[reason]++
}

func (collector *susCollector) collectErrorCounters(ch chan<- prometheus.Metric) {
	collector.mu.Lock()
	defer collector.mu.Unlock()

	for reason, value := range collector.errors {
		ch <- prometheus.MustNewConstMetric(collector.scrapeErrors, prometheus.CounterValue, value, reason)
	}
}

func classifyError(err error) string {
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "read pins"):
		return "read_pins"
	case strings.Contains(message, "read load"):
		return "read_load"
	case strings.Contains(message, "summarize"):
		return "summarize"
	default:
		return "unknown"
	}
}

func main() {
	listen := flag.String("listen", "127.0.0.1:9479", "HTTP listen address")
	metricsPath := flag.String("path", "/metrics", "Metrics path")
	flag.Parse()

	logger := log.New(os.Stderr, "sus-exporter: ", log.LstdFlags)

	ret := nvml.Init()
	if ret != nvml.SUCCESS {
		logger.Printf("nvmlInit failed: %v", ret)
	} else {
		defer nvml.Shutdown()
	}

	if !isLoopbackListenAddress(*listen) {
		logger.Printf("warning: exporter is listening beyond loopback on %s; restrict access with a firewall", *listen)
	}

	registry := prometheus.NewRegistry()
	registry.MustRegister(newSusCollector(hardwareReader{}, logger))
	registry.MustRegister(newGPUAvailabilityCollector(hardwareGPUReader{}))

	mux := http.NewServeMux()
	mux.Handle(*metricsPath, promhttp.HandlerFor(registry, promhttp.HandlerOpts{
		ErrorLog:      logger,
		ErrorHandling: promhttp.ContinueOnError,
	}))
	mux.HandleFunc("/healthz", func(response http.ResponseWriter, request *http.Request) {
		_, _ = response.Write([]byte("OK\n"))
	})

	server := &http.Server{
		Addr:              *listen,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       30 * time.Second,
	}

	logger.Printf("listening on %s", *listen)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Printf("server failed: %v", err)
		os.Exit(1)
	}
}

func isLoopbackListenAddress(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
