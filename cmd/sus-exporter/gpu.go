package main

import (
	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/prometheus/client_golang/prometheus"
)

type gpuReader interface {
	Count() (int, nvml.Return)
	ReadMemory(int) nvml.Return
}

type hardwareGPUReader struct{}

func (hardwareGPUReader) Count() (int, nvml.Return) { return nvml.DeviceGetCount() }

func (hardwareGPUReader) ReadMemory(index int) nvml.Return {
	device, ret := nvml.DeviceGetHandleByIndex(index)
	if ret != nvml.SUCCESS {
		return ret
	}
	_, ret = device.GetMemoryInfo()
	return ret
}

func newGPUAvailabilityCollector(reader gpuReader) prometheus.Collector {
	return prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "sus_gpu_available",
		Help: "Whether NVML can read memory on every visible GPU; 0 also means no GPU or a driver error.",
	}, func() float64 {
		count, ret := reader.Count()
		if ret != nvml.SUCCESS || count == 0 {
			return 0
		}
		for index := 0; index < count; index++ {
			if reader.ReadMemory(index) != nvml.SUCCESS {
				return 0
			}
		}
		return 1
	})
}
