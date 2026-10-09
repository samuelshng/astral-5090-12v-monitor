package main

import (
	"testing"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/prometheus/client_golang/prometheus"
)

type fakeGPUReader struct {
	count  int
	ret    nvml.Return
	memory []nvml.Return
}

func (reader fakeGPUReader) Count() (int, nvml.Return)        { return reader.count, reader.ret }
func (reader fakeGPUReader) ReadMemory(index int) nvml.Return { return reader.memory[index] }

func TestGPUAvailability(t *testing.T) {
	for _, test := range []struct {
		name   string
		reader fakeGPUReader
		want   float64
	}{
		{"healthy", fakeGPUReader{1, nvml.SUCCESS, []nvml.Return{nvml.SUCCESS}}, 1},
		{"all visible GPUs", fakeGPUReader{2, nvml.SUCCESS, []nvml.Return{nvml.SUCCESS, nvml.SUCCESS}}, 1},
		{"lost GPU", fakeGPUReader{1, nvml.SUCCESS, []nvml.Return{nvml.ERROR_GPU_IS_LOST}}, 0},
		{"unknown driver error", fakeGPUReader{1, nvml.SUCCESS, []nvml.Return{nvml.ERROR_UNKNOWN}}, 0},
		{"one of two lost", fakeGPUReader{2, nvml.SUCCESS, []nvml.Return{nvml.SUCCESS, nvml.ERROR_GPU_IS_LOST}}, 0},
		{"empty discovery", fakeGPUReader{0, nvml.SUCCESS, nil}, 0},
		{"discovery error", fakeGPUReader{0, nvml.ERROR_UNKNOWN, nil}, 0},
		{"driver not initialized", fakeGPUReader{0, nvml.ERROR_UNINITIALIZED, nil}, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			registry := prometheus.NewRegistry()
			registry.MustRegister(newGPUAvailabilityCollector(test.reader))
			got, err := registry.Gather()
			if err != nil || len(got) != 1 || got[0].GetName() != "sus_gpu_available" || got[0].Metric[0].Gauge.GetValue() != test.want {
				t.Fatalf("GPU availability = %v, %v; want %v", got, err, test.want)
			}
		})
	}
}
