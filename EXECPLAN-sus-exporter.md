# Build a Read-Only Prometheus Exporter for ASUS Astral RTX 5090 Pin Telemetry

This ExecPlan is a living document. The sections `Progress`, `Surprises & Discoveries`, `Decision Log`, and `Outcomes & Retrospective` must be kept up to date as work proceeds.

This repository does not currently contain a `PLANS.md` file. This plan follows the ExecPlan style described in the OpenAI Cookbook article "Using PLANS.md for multi-hour problem solving": it is self-contained, outcome-focused, and written so that a contributor with only this file and the repository can implement the feature.

## Purpose / Big Picture

After this work, each Linux machine with a supported ASUS ROG Astral RTX 5090 can run a read-only monitoring process that exposes per-pin 12V-2x6 connector telemetry to Prometheus. A central Prometheus instance on the LAN can scrape all machines, Grafana can show a single dashboard across hosts, and Alertmanager can later send email alerts when pin power, current, voltage, or imbalance crosses safety thresholds.

This change deliberately avoids automatic mitigation. The existing `susd` daemon can lower GPU power limit or lock clocks through NVML when it detects overload or imbalance. That behavior is useful as a separate experiment, but this plan creates telemetry plumbing only: observe, record, dashboard, and alert. A user can see the work succeed by starting the exporter, opening `http://127.0.0.1:9479/metrics`, and seeing six labeled pin readings plus connector-level summary metrics for each detected GPU. For safety, the exporter defaults to loopback-only listening; exposing it to a central LAN Prometheus server is an explicit deployment choice.

## Progress

- [x] (2026-06-27 12:00 America/Vancouver) Confirmed the local GPU is a supported ASUS Astral RTX 5090 with PCI device `10de:2b85` and subsystem `1043:89e3`.
- [x] (2026-06-27 12:00 America/Vancouver) Confirmed upstream `susm` is read-only and upstream `susd` performs active mitigation by setting power limits or locked clocks.
- [x] (2026-06-27 12:00 America/Vancouver) Chose the fork direction: a Prometheus exporter first, with threshold rules and notifications owned by Prometheus and Alertmanager rather than this repository.
- [x] (2026-06-27 12:20 America/Vancouver) Completed adversarial subagent review and revised this plan for NVML lifecycle, partial-device error handling, Prometheus metrics, service security, hardware permissions, Makefile idempotence, validation, and scrape interval.
- [x] (2026-06-27 13:55 America/Vancouver) Created reusable telemetry snapshot API with per-device result handling.
- [x] (2026-06-27 13:55 America/Vancouver) Added `cmd/sus-exporter` as a read-only HTTP server using the official Prometheus Go client.
- [x] (2026-06-27 13:55 America/Vancouver) Added `cmd/sus-check` as a one-shot diagnostic command with i2c permission hints.
- [x] (2026-06-27 13:55 America/Vancouver) Added hardware-free tests for summary math, invalid input, Prometheus collection, fake telemetry, and concurrent scrapes.
- [x] (2026-06-27 13:55 America/Vancouver) Added systemd service, README install notes, Prometheus scrape example, and alert rule examples.
- [x] (2026-06-27 13:55 America/Vancouver) Validated local `/healthz` and `/metrics` smoke test. `promtool` was not installed locally, so `promtool check metrics` remains unrun.
- [x] (2026-10-05) Verified both studysn and studysn5090 from homelab-ops Prometheus, with separate host labels and six successful pin readings each.

## Surprises & Discoveries

- Observation: The upstream `sus` code identifies compatible GPUs with NVML and then locates an NVIDIA i2c adapter under the GPU PCI sysfs path.
  Evidence: `sus.go` contains `FindAstralDevices`, checks `info.PciDeviceId` and `info.PciSubSystemId`, and calls `findAstralDeviceSensorNumber`.

- Observation: Access to `/dev/i2c-*` may require root on Ubuntu.
  Evidence: On the first target machine, `/dev/i2c-0` through `/dev/i2c-5` were owned by `root:root` with mode `0600`, and running `susm` without sudo returned `permission denied`.

- Observation: The existing Makefile builds only `bin/susm` and `bin/susd` and does not install services or exporters.
  Evidence: `Makefile` has targets for `bin/susm`, `bin/susd`, and `clean`.

- Observation: The existing Makefile runs `go get` inside build targets, which can mutate `go.mod` or `go.sum` during ordinary builds.
  Evidence: The current `Makefile` invokes `go get` before `go build` for both existing binaries.

- Observation: Existing commands own NVML lifecycle explicitly, but the first version of this plan did not say who initializes NVML for new commands.
  Evidence: `susm/main.go` and `susd/main.go` call `nvml.Init()` before using package `sus` and defer `nvml.Shutdown()`.

## Decision Log

- Decision: Implement a read-only Prometheus exporter as the primary product of the fork.
  Rationale: Multiple GPU hosts should be aggregated by a LAN monitoring stack. Prometheus is designed to scrape HTTP metrics from many targets, store numeric time series, and evaluate alert rules centrally.
  Date/Author: 2026-06-27 / Samuel and Codex

- Decision: Keep alert thresholds outside the exporter.
  Rationale: Thresholds will evolve as real data is collected from both machines. Keeping them in Prometheus rules allows safe iteration without rebuilding or redeploying the hardware reader.
  Date/Author: 2026-06-27 / Samuel and Codex

- Decision: Do not make this fork send email directly.
  Rationale: Email credentials, grouping, repeat intervals, silencing, and routing are Alertmanager responsibilities. Encoding them in the exporter would create duplicate alerting logic and a larger secret-handling surface.
  Date/Author: 2026-06-27 / Samuel and Codex

- Decision: Treat active mitigation as out of scope for this plan.
  Rationale: Automatic power-limit and clock-locking behavior can be valuable, but it changes GPU operating state. This plan is for trustworthy observability first.
  Date/Author: 2026-06-27 / Samuel and Codex

- Decision: Add a `sus-check` command even though `sus-exporter` is the main deliverable.
  Rationale: Hardware access issues are likely during setup. A one-shot diagnostic command gives a simple way to verify supported GPUs, i2c adapter discovery, permissions, and sample readings before running a service.
  Date/Author: 2026-06-27 / Samuel and Codex

- Decision: Require the official Prometheus Go client and a custom collector rather than manually formatting metrics.
  Rationale: The official client handles the Prometheus exposition format, integrates with `promhttp`, and makes invalid metric output less likely. A custom collector can call the hardware snapshot reader on each scrape while remaining testable with a fake reader.
  Date/Author: 2026-06-27 / Codex after adversarial review

- Decision: Default the exporter to `127.0.0.1:9479` and require operators to opt in to LAN exposure.
  Rationale: The exporter may initially need to run as root to read `/dev/i2c-N`. Binding a root process to all interfaces by default is unnecessarily risky. A central Prometheus deployment can still scrape the exporter after the operator explicitly binds to a LAN IP and applies firewall allowlisting.
  Date/Author: 2026-06-27 / Codex after adversarial review

- Decision: Start example Prometheus scraping at `5s`, not `1s`.
  Rationale: Five-second scraping is still responsive for connector safety monitoring while reducing SMBus traffic, log volume, and failure noise. Faster scraping can be evaluated later after baseline data is collected.
  Date/Author: 2026-06-27 / Codex after adversarial review

## Outcomes & Retrospective

Implementation milestone 2026-06-27: The read-only snapshot API, `sus-exporter`, `sus-check`, Makefile targets, service file, README guidance, and tests are implemented. Local tests, race tests, Makefile build, `sus-check` diagnostic failure path, and exporter `/healthz` and `/metrics` smoke tests passed. Remaining work is deployment validation under sudo or service context so real pin readings can be scraped, and central Prometheus validation across two GPU hosts.

Expected final outcome: a contributor can build the fork, install `sus-exporter` on each GPU host, configure one central Prometheus instance to scrape both hosts, and observe per-pin telemetry and alert state from a single Grafana dashboard without running any automatic mitigation daemon.

## Context and Orientation

This repository is a small Go module named `github.com/jan-provaznik/sus`. It currently contains one library file and two commands:

- `sus.go` contains the core package `sus`. It uses NVIDIA NVML to find supported GPUs and Linux SMBus/i2c to read six voltage/current pairs from the ASUS Astral 12V-2x6 monitoring chip.
- `susm/main.go` is a simple terminal monitor. It loops forever, reads all compatible devices, and prints total GPU load plus six per-pin power readings.
- `susd/main.go` is a daemon. A daemon is a long-running background process. This daemon reads the same pins but also calls NVML setters to reduce power draw or lock GPU clocks when limits are exceeded.
- `install/susd.service` is a systemd service file for `susd`. systemd is the Linux service manager used to start and supervise background services.
- `Makefile` builds `bin/susm` and `bin/susd`.

The main non-obvious terms used in this plan are:

Prometheus: an open-source monitoring system that periodically scrapes HTTP endpoints and stores numeric measurements over time.

Exporter: a small process that exposes measurements in Prometheus's text format at an HTTP endpoint, usually `/metrics`.

Metric: one named numeric measurement, such as `sus_pin_power_watts`.

Label: a key/value attribute attached to a metric, such as `pin="3"` or `gpu_uuid="GPU-..."`. Labels let Prometheus group and filter measurements. Avoid labels with high cardinality, meaning labels that can take many unpredictable values such as raw error messages or timestamps.

Alertmanager: the Prometheus component that receives firing alerts and sends notifications such as email.

Grafana: a dashboard tool that queries Prometheus and visualizes time series.

NVML: NVIDIA Management Library, the NVIDIA API used here to enumerate GPUs and read GPU-level power data. NVML has global process lifecycle: each command must call `nvml.Init()` before using package `sus` hardware functions and `nvml.Shutdown()` before exit after successful initialization.

i2c/SMBus: low-level hardware buses used here to read the ASUS per-pin sensor. On Linux the bus is exposed through device files such as `/dev/i2c-0`. Bus numbers can differ by host and boot, so the diagnostic command must print the actual bus it selected.

12V-2x6: the GPU power connector family used by modern high-power NVIDIA cards. The ASUS Astral card exposes measurements for six 12V pins; this project reads voltage and current for each of those six pins.

## Plan of Work

First, refactor the current reading path into a reusable snapshot API in `sus.go`. The current command code repeats aggregation logic in `susm/main.go` and `susd/main.go`. Add exported types that describe a complete device reading: GPU identity, NVML index, PCI sysfs path, i2c bus number, `/dev/i2c-N` path, NVML-reported GPU load, six pin readings, total connector draw, minimum pin draw, maximum pin draw, and balance ratio. The balance ratio is `min_pin_power / max_pin_power`. A value near `1.0` means pins are sharing load evenly. A lower value means one or more pins are carrying much less or much more than the others.

The new API should not change the existing low-level functions unless necessary. Keep `FindAstralDevices`, `ReadAstralDevicePins`, and `ReadAstralDeviceLoad` available. Add `Index() int`, `I2CBusNumber() int`, `I2CDevicePath() string`, and `PCISysfsPath() string` accessors on `AstralDevice`. `Index()` must mean the original NVML index passed to `nvml.DeviceGetHandleByIndex`, not the index inside the filtered list of compatible devices. The GPU UUID remains the stable identity; the NVML index is only a familiar local label.

Add a summary helper, for example `SummarizePins(pins []AstralDevicePin) (AstralPinSummary, error)`. It must require exactly six pins, reject negative voltage or current, reject non-finite values such as NaN or infinity, and return an error if maximum pin power is zero so the balance ratio cannot become NaN or infinity. Exporter code must never emit non-finite Prometheus samples.

Add a new function, for example `ReadAstralDeviceSnapshot(device AstralDevice) (AstralDeviceSnapshot, error)`, that combines pin reads, load reads, and summary calculation for one GPU. For multi-GPU reads, do not make one failing device hide healthy devices. Either implement `ReadAllAstralDeviceSnapshots() []AstralDeviceSnapshotResult`, where each result contains `Device`, `Snapshot`, and `Err`, or have `sus-exporter` call `FindAstralDevices()` and then iterate devices independently. This plan prefers the explicit result type:

    type AstralDeviceSnapshotResult struct {
        Device AstralDevice
        Snapshot AstralDeviceSnapshot
        Err error
    }

Library functions in package `sus` do not own NVML global lifecycle. Each command that uses NVML must call `nvml.Init()` before any hardware call and `nvml.Shutdown()` during exit after successful initialization. Existing `susm` and `susd` already do this and should keep doing it. New `sus-exporter` and `sus-check` must fail fast if `nvml.Init()` fails and must not attempt hardware reads before initialization.

Second, add a new command in `cmd/sus-exporter/main.go`. This is an intentional new command layout: new commands go under `cmd/`, while existing `susm/` and `susd/` stay in their current directories for compatibility. This command starts an HTTP server. By default it listens on `127.0.0.1:9479` and exposes:

- `/metrics`, returning Prometheus text exposition format through `promhttp`.
- `/healthz`, returning HTTP 200 with body `OK` when the process is running.

The exporter should read hardware on each scrape rather than keeping its own background loop. This keeps behavior simple: when Prometheus scrapes `/metrics`, the custom collector calls the snapshot API, exports latest readings for devices that succeeded, exports per-device failure gauges for devices that failed, and returns valid Prometheus output. Use `github.com/prometheus/client_golang/prometheus` and `github.com/prometheus/client_golang/prometheus/promhttp`; do not manually format metrics. Implement the collector behind an interface so tests can use fake telemetry without NVIDIA hardware.

The exporter must use a real `http.Server` with timeouts, not `http.ListenAndServe` with defaults. Set at least `ReadHeaderTimeout`, `ReadTimeout`, `WriteTimeout`, and `IdleTimeout` to small, documented values. If a scrape fails, log the detailed error to stderr and increment a persistent `sus_scrape_errors_total{reason="..."}` counter. Do not create a new counter inside the scrape handler. The allowed `reason` label values must be low-cardinality and stable, for example `find_devices`, `read_pins`, `read_load`, `summarize`, and `unknown`. Do not use raw error strings as labels.

Third, add a new command in `cmd/sus-check/main.go`. This command performs one read and prints human-friendly diagnostics. It should list detected devices, the UUID, NVML index, PCI sysfs path, selected i2c adapter number, `/dev/i2c-N` path, per-pin voltage/current/power, total connector power, min/max pin power, and balance ratio. It should exit nonzero if no compatible GPUs are found or if pin reads fail. It should include a clear permission hint when the error is `permission denied`, for example: "Could not open /dev/i2c-N; try running with sudo or install a udev rule that grants the service account access." When i2c access fails, wrap the error with the actual `/dev/i2c-N` path and PCI sysfs path.

Fourth, update the Makefile. Keep existing `susm` and `susd` targets working for compatibility, but add `bin/sus-exporter` and `bin/sus-check`. Make `all` build all four binaries unless a deliberate decision is made to stop building `susd`. Remove `go get` from build targets. Dependency changes should be made explicitly with `go get github.com/prometheus/client_golang@<chosen-version>` once, followed by `go mod tidy`; normal builds should run only `go build`. If keeping `susd`, add documentation that it is an active mitigation daemon and should not be enabled unless the operator intends that behavior.

Fifth, add tests that do not require NVIDIA hardware. The main hardware functions cannot be exercised in CI without a GPU and i2c sensor, so isolate pure logic for testing. Add tests for total draw, min draw, max draw, balance ratio, wrong pin count, zero-power input, negative input, non-finite input, Prometheus collection with fake telemetry, and concurrent scrapes. Tests should run with `go test ./...` on any development machine. Also run `go test -race ./...` before accepting the exporter because Prometheus may scrape concurrently.

Sixth, add documentation and install examples. Update the existing `README` and do not create a duplicate primary README unless the same change renames `README` to `README.md`. Include separate sections for:

- What this exporter does.
- What it deliberately does not do.
- How to build.
- How to run a one-shot check.
- How to run the exporter manually.
- How to install a systemd service for `sus-exporter`.
- How to configure Prometheus scrape targets for one or more GPU hosts.
- Example Prometheus alert rules for warning and critical pin power, current, voltage, imbalance, and exporter availability conditions.
- A short note that email notification belongs in Alertmanager and is intentionally not configured by the exporter.

Seventh, add install assets. Create `install/sus-exporter.service` as a systemd unit that runs `/usr/local/bin/sus-exporter --listen=127.0.0.1:9479` by default. The unit may need to run as root initially because `/dev/i2c-*` is commonly root-only. If a later udev rule is added, the service can be changed to a less privileged user. Do not install or enable the service automatically from the Makefile; document explicit commands instead.

Eighth, document the service security model. Running as root and binding to the network is risky, so the default bind address is loopback only. For central Prometheus scraping, the operator must deliberately set `--listen=<LAN-IP>:9479` or use a local reverse proxy/tunnel, then allow only the Prometheus host through the firewall. The systemd unit must include hardening that is compatible with i2c/NVML access: `NoNewPrivileges=true`, `ProtectSystem=strict`, `ProtectHome=true`, `PrivateTmp=true`, and `CapabilityBoundingSet=`. Do not set `PrivateDevices=true` while the service needs `/dev/i2c-N` and NVIDIA device files.

## Concrete Steps

Work from the repository root:

    cd /home/samuelshng/git/sus

Before editing, inspect the current files:

    sed -n '1,260p' sus.go
    sed -n '1,220p' susm/main.go
    sed -n '1,260p' susd/main.go
    sed -n '1,160p' Makefile

Add the Prometheus dependency once, then remove `go get` from Makefile build rules:

    PATH=/home/samuelshng/.local/go1.25.6/bin:$PATH go get github.com/prometheus/client_golang@<chosen-version>
    PATH=/home/samuelshng/.local/go1.25.6/bin:$PATH go mod tidy

Add snapshot and summary types to `sus.go`. The final shape does not need to match these names exactly, but it must expose equivalent information:

    type AstralPinSummary struct {
        TotalWatts float64
        MinWatts float64
        MaxWatts float64
        BalanceRatio float64
    }

    type AstralDeviceSnapshot struct {
        Device AstralDevice
        LoadWatts float64
        Pins []AstralDevicePin
        Summary AstralPinSummary
    }

    type AstralDeviceSnapshotResult struct {
        Device AstralDevice
        Snapshot AstralDeviceSnapshot
        Err error
    }

    func SummarizePins(pins []AstralDevicePin) (AstralPinSummary, error)
    func ReadAstralDeviceSnapshot(device AstralDevice) (AstralDeviceSnapshot, error)
    func ReadAllAstralDeviceSnapshots() ([]AstralDeviceSnapshotResult, error)

The second return from `ReadAllAstralDeviceSnapshots` should be used only for errors that prevent device enumeration entirely, such as `FindAstralDevices` failing. Per-device read errors belong in each result's `Err` field.

Because `AstralDevicePin` currently has unexported fields and exported accessor methods, tests should either use a constructor helper in tests within package `sus` or add a small exported constructor only if it is genuinely useful outside tests. Prefer tests in package `sus` so they can construct values without adding API surface.

Create `cmd/sus-exporter/main.go`. It should parse at least these flags:

    --listen=127.0.0.1:9479
    --path=/metrics

On startup it should call `nvml.Init()`. If initialization fails, print `nvmlInit failed` plus the returned status and exit nonzero. If it succeeds, defer `nvml.Shutdown()` and start the HTTP server. On startup it should log the listen address and warn when the address is not loopback. On `/healthz`, it should return:

    HTTP 200
    OK

On `/metrics`, a successful response should contain metrics like this, with real numbers substituted:

    # HELP sus_pin_voltage_volts Voltage measured at an ASUS Astral 12V-2x6 connector pin.
    # TYPE sus_pin_voltage_volts gauge
    sus_pin_voltage_volts{gpu_uuid="GPU-da30543b-2187-0988-ac45-81150dd69eec",gpu_index="0",pin="1"} 12.063
    # HELP sus_pin_current_amps Current measured at an ASUS Astral 12V-2x6 connector pin.
    # TYPE sus_pin_current_amps gauge
    sus_pin_current_amps{gpu_uuid="GPU-da30543b-2187-0988-ac45-81150dd69eec",gpu_index="0",pin="1"} 4.221
    # HELP sus_pin_power_watts Power calculated as voltage times current for one ASUS Astral 12V-2x6 connector pin.
    # TYPE sus_pin_power_watts gauge
    sus_pin_power_watts{gpu_uuid="GPU-da30543b-2187-0988-ac45-81150dd69eec",gpu_index="0",pin="1"} 50.918
    # HELP sus_connector_power_watts Sum of the six measured 12V pin powers.
    # TYPE sus_connector_power_watts gauge
    sus_connector_power_watts{gpu_uuid="GPU-da30543b-2187-0988-ac45-81150dd69eec",gpu_index="0"} 305.500
    # HELP sus_pin_balance_ratio Ratio of minimum pin power to maximum pin power.
    # TYPE sus_pin_balance_ratio gauge
    sus_pin_balance_ratio{gpu_uuid="GPU-da30543b-2187-0988-ac45-81150dd69eec",gpu_index="0"} 0.874
    # HELP sus_device_read_success Whether reading this GPU succeeded during the scrape, 1 for success and 0 for failure.
    # TYPE sus_device_read_success gauge
    sus_device_read_success{gpu_uuid="GPU-da30543b-2187-0988-ac45-81150dd69eec",gpu_index="0"} 1
    # HELP sus_scrape_errors_total Total scrape errors by stable reason.
    # TYPE sus_scrape_errors_total counter
    sus_scrape_errors_total{reason="read_pins"} 0

Create `cmd/sus-check/main.go`. A successful run should look roughly like this:

    Detected 1 compatible ASUS Astral RTX 5090 device.
    Device 0: GPU-da30543b-2187-0988-ac45-81150dd69eec
    NVML index: 0
    PCI sysfs path: /sys/bus/pci/devices/0000:06:00.0
    i2c device: /dev/i2c-0
    Total NVML GPU load: 321.4 W
    Total connector draw: 305.5 W
    Pin draw min/max: 48.9 W / 56.0 W
    Balance ratio: 0.87
    Pin 1: 12.063 V 4.221 A 50.9 W
    Pin 2: 12.061 V 4.102 A 49.5 W
    Pin 3: 12.058 V 4.641 A 56.0 W
    Pin 4: 12.060 V 4.052 A 48.9 W
    Pin 5: 12.059 V 4.411 A 53.2 W
    Pin 6: 12.062 V 4.229 A 51.0 W

Update `Makefile` so these commands build all binaries without running `go get` during build:

    bin/susm: susm/main.go $(DEPS) | bin
        go build -o $@ ./susm

    bin/susd: susd/main.go $(DEPS) | bin
        go build -o $@ ./susd

    bin/sus-exporter: cmd/sus-exporter/main.go $(DEPS) | bin
        go build -o $@ ./cmd/sus-exporter

    bin/sus-check: cmd/sus-check/main.go $(DEPS) | bin
        go build -o $@ ./cmd/sus-check

Build:

    PATH=/home/samuelshng/.local/go1.25.6/bin:$PATH make

If Go is installed globally, this shorter command is fine:

    make

Run tests:

    PATH=/home/samuelshng/.local/go1.25.6/bin:$PATH go test ./...
    PATH=/home/samuelshng/.local/go1.25.6/bin:$PATH go test -race ./...

Run the check command on a GPU host. It may require sudo because of `/dev/i2c-*` permissions:

    sudo ./bin/sus-check

If it fails with an i2c or permission error, diagnose with commands like these, replacing `N` with the bus printed by `sus-check`:

    ls -l /dev/i2c-N
    lsmod | grep i2c_dev
    sudo modprobe i2c-dev
    nvidia-smi

Run the exporter manually in loopback-only mode:

    sudo ./bin/sus-exporter --listen=127.0.0.1:9479

In another terminal on the same host:

    curl -fsS http://127.0.0.1:9479/healthz
    curl -fsS http://127.0.0.1:9479/metrics
    curl -fsS http://127.0.0.1:9479/metrics | promtool check metrics

Install manually only after local validation:

    sudo install -m 0755 bin/sus-exporter /usr/local/bin/sus-exporter
    sudo install -m 0755 bin/sus-check /usr/local/bin/sus-check
    sudo install -m 0644 install/sus-exporter.service /etc/systemd/system/sus-exporter.service
    sudo systemctl daemon-reload
    sudo systemctl enable --now sus-exporter
    systemctl status sus-exporter

Add a central Prometheus scrape config on the LAN monitoring host only after explicitly exposing each exporter to the LAN. Replace hostnames with the real GPU hostnames or IP addresses, bind each exporter to a LAN IP, and firewall port `9479` so only the Prometheus host can connect:

    scrape_configs:
      - job_name: sus-exporter
        scrape_interval: 5s
        static_configs:
          - targets:
              - gpu-host-a.lan:9479
              - gpu-host-b.lan:9479

Add initial Prometheus alert rules outside this repository or in a documented example file. These are starting points, not final safety science:

    groups:
      - name: sus-connector-alerts
        rules:
          - alert: SusPinPowerWarning
            expr: sus_pin_power_watts > 105
            for: 1m
            labels:
              severity: warning
            annotations:
              summary: ASUS Astral pin power warning on {{ $labels.instance }} pin {{ $labels.pin }}
              description: A 12V-2x6 pin has exceeded 105 W for 1 minute.
          - alert: SusPinPowerCritical
            expr: sus_pin_power_watts > 110
            for: 15s
            labels:
              severity: critical
            annotations:
              summary: ASUS Astral pin power critical on {{ $labels.instance }} pin {{ $labels.pin }}
              description: A 12V-2x6 pin has exceeded 110 W for 15 seconds.
          - alert: SusPinCurrentCritical
            expr: sus_pin_current_amps > 9.2
            for: 15s
            labels:
              severity: critical
            annotations:
              summary: ASUS Astral pin current critical on {{ $labels.instance }} pin {{ $labels.pin }}
              description: A 12V-2x6 pin current reading has exceeded 9.2 A for 15 seconds.
          - alert: SusPinVoltageLow
            expr: sus_pin_voltage_volts < 11.4
            for: 15s
            labels:
              severity: warning
            annotations:
              summary: ASUS Astral pin voltage low on {{ $labels.instance }} pin {{ $labels.pin }}
              description: A 12V pin voltage reading has stayed below 11.4 V for 15 seconds.
          - alert: SusPinImbalanceCritical
            expr: sus_pin_balance_ratio < 0.75 and sus_connector_power_watts > 300
            for: 15s
            labels:
              severity: critical
            annotations:
              summary: ASUS Astral connector load imbalance on {{ $labels.instance }}
              description: Minimum pin power divided by maximum pin power is below 0.75 while connector draw is above 300 W.
          - alert: SusDeviceReadFailure
            expr: sus_device_read_success == 0
            for: 30s
            labels:
              severity: critical
            annotations:
              summary: sus-exporter cannot read GPU telemetry on {{ $labels.instance }}
              description: The exporter is reachable but cannot read one ASUS Astral GPU.
          - alert: SusExporterDown
            expr: up{job="sus-exporter"} == 0
            for: 30s
            labels:
              severity: critical
            annotations:
              summary: sus-exporter is down on {{ $labels.instance }}
              description: Prometheus cannot scrape ASUS Astral pin telemetry from this host.

## Validation and Acceptance

The first acceptance condition is that the repository still builds and tests on a machine without requiring GPU hardware for tests:

    cd /home/samuelshng/git/sus
    PATH=/home/samuelshng/.local/go1.25.6/bin:$PATH go test ./...
    PATH=/home/samuelshng/.local/go1.25.6/bin:$PATH go test -race ./...

Expected outcome: all packages pass. If there are no tests in a command package, Go may print `?` with `[no test files]`; that is acceptable. Any compile error, failing test, or race failure is not acceptable.

The second acceptance condition is that `sus-check` demonstrates real hardware access on a supported GPU host:

    sudo ./bin/sus-check

Expected outcome: it prints at least one compatible device, NVML index, PCI sysfs path, `/dev/i2c-N`, six pin rows, total connector draw, and a balance ratio. If it exits with `permission denied`, the code should print a clear permission hint. If it prints "Could not find any compatible devices" on a known ASUS Astral RTX 5090 host, detection is broken and must be fixed before proceeding.

The third acceptance condition is that the exporter exposes valid Prometheus metrics:

    sudo ./bin/sus-exporter --listen=127.0.0.1:9479
    curl -fsS http://127.0.0.1:9479/healthz
    curl -fsS http://127.0.0.1:9479/metrics
    curl -fsS http://127.0.0.1:9479/metrics | promtool check metrics

Expected `/healthz` body:

    OK

Expected `/metrics` contents include at least these metric names:

    sus_pin_voltage_volts
    sus_pin_current_amps
    sus_pin_power_watts
    sus_connector_power_watts
    sus_pin_balance_ratio
    sus_device_read_success
    sus_scrape_errors_total

The fourth acceptance condition is central aggregation. On a LAN Prometheus instance configured to scrape two GPU hosts, query:

    up{job="sus-exporter"}

Expected outcome: two time series return value `1`, one for each host. Query:

    sus_pin_power_watts

Expected outcome: twelve pin power series for two single-GPU hosts, with Prometheus's `instance` label identifying the host and the exporter labels `gpu_uuid`, `gpu_index`, and `pin` identifying the GPU and connector pin.

The fifth acceptance condition is alert rule behavior. In Prometheus, use the rules UI or `promtool check rules` against the example rules file. The syntax must validate. If a local test series is added later, include a `promtool test rules` file that proves warning and critical alerts fire only after their configured `for` durations.

## Idempotence and Recovery

All code changes should be additive and safe to rebuild repeatedly. Running `make` multiple times should leave the same binaries in `bin/`. Running `go test ./...` should not modify system files. Normal Makefile targets must not run `go get`.

The exporter must be safe to restart. If the process exits, systemd can restart it without changing GPU state because the exporter never calls NVML setter functions. Do not call `LimitAstralDeviceFreq`, `LimitAstralDeviceLoad`, `nvml.DeviceSetPowerManagementLimit`, or `nvml.DeviceSetGpuLockedClocks` anywhere in `cmd/sus-exporter` or shared code used by the exporter.

Manual install commands using `sudo install` overwrite the target binary atomically enough for this use case and can be repeated after each build. If the systemd service fails to start, inspect logs:

    journalctl -u sus-exporter -n 100 --no-pager

Common recovery steps are:

- If logs show `permission denied`, run `sus-check` to see the exact `/dev/i2c-N` path, inspect it with `ls -l /dev/i2c-N`, and either run the service as root temporarily or add a later udev rule to grant read access to the required device.
- If logs show `nvmlInit failed`, confirm the NVIDIA driver and `nvidia-smi` work.
- If logs show the i2c device is missing, confirm the kernel exposes i2c-dev support with `lsmod | grep i2c_dev` or try `sudo modprobe i2c-dev`. Some kernels build `i2c-dev` in, so a missing `lsmod` entry is not conclusive if `/dev/i2c-N` exists.
- If Prometheus cannot scrape the host, confirm the exporter is deliberately bound to the expected LAN IP and host firewall rules allow TCP port `9479` only from the monitoring host.

If a change accidentally wires active mitigation into the exporter, revert that change before installing. The observable sign of a violation is any call path from `cmd/sus-exporter` to `LimitAstralDeviceFreq`, `LimitAstralDeviceLoad`, or NVML setter APIs.

## Artifacts and Notes

The upstream `susd` default thresholds are useful context but not policy. It defaults to `105.0` W maximum power draw per wire and `0.75` minimum balance ratio. This plan uses those as initial example alert values because they are conservative relative to an evenly distributed 600 W connector load. They must be treated as starting points and revisited after collecting baseline data from both machines.

For a 600 W connector load distributed evenly across six 12 V pins, the rough per-pin current is:

    600 W / 6 pins / 12 V = 8.33 A per pin

If a pin carries 105 W at 12 V, the rough current is:

    105 W / 12 V = 8.75 A

If a pin carries 110 W at 12 V, the rough current is:

    110 W / 12 V = 9.17 A

These calculations are simple operating heuristics, not a formal connector certification. Keep the alert examples in documentation clearly labeled as initial monitoring policy.

The systemd service file should be similar to:

    [Unit]
    Description=ASUS Astral RTX 5090 pin telemetry Prometheus exporter
    After=network-online.target
    Wants=network-online.target

    [Service]
    Type=simple
    ExecStart=/usr/local/bin/sus-exporter --listen=127.0.0.1:9479
    Restart=on-failure
    RestartSec=2s
    NoNewPrivileges=true
    ProtectSystem=strict
    ProtectHome=true
    PrivateTmp=true
    CapabilityBoundingSet=

    [Install]
    WantedBy=multi-user.target

Do not set `PrivateDevices=true` in this initial unit because the service needs access to `/dev/i2c-N` and NVIDIA device files. If a later least-privilege design grants device access through a dedicated group and udev rules, revisit the unit hardening then.

The final README should warn clearly:

    sus-exporter and sus-check are read-only monitoring tools. They do not change GPU power limits or clocks. The upstream susd daemon performs active mitigation and should not be enabled unless you explicitly want that behavior.

## Interfaces and Dependencies

Use the existing Go module. Keep the module path unless the fork intentionally renames it later:

    module github.com/jan-provaznik/sus

The existing dependencies are:

- `github.com/NVIDIA/go-nvml/pkg/nvml` for GPU discovery and NVML power readings.
- `github.com/khirono/go-i2c/smbus` for SMBus reads from the ASUS sensor.

Add the official Prometheus Go client:

    github.com/prometheus/client_golang/prometheus
    github.com/prometheus/client_golang/prometheus/promhttp

Using the official client reduces formatting mistakes and makes `/metrics` output standard. Implement a custom collector backed by a telemetry reader interface so hardware access can be faked in tests.

At the end of the implementation, these commands should exist:

- `bin/susm`: existing terminal monitor.
- `bin/susd`: existing active mitigation daemon, documented as such.
- `bin/sus-check`: new one-shot read-only diagnostic command.
- `bin/sus-exporter`: new read-only Prometheus exporter.

At the end of the implementation, these HTTP endpoints should exist in `sus-exporter`:

- `GET /healthz`: returns HTTP 200 with body `OK` when the process is alive.
- `GET /metrics`: returns Prometheus metrics from a fresh hardware read, or a valid metrics response marking per-device read failure if a device read fails.

At the end of the implementation, these Prometheus metric names should exist:

- `sus_pin_voltage_volts`, gauge, labels `gpu_uuid`, `gpu_index`, `pin`.
- `sus_pin_current_amps`, gauge, labels `gpu_uuid`, `gpu_index`, `pin`.
- `sus_pin_power_watts`, gauge, labels `gpu_uuid`, `gpu_index`, `pin`.
- `sus_connector_power_watts`, gauge, labels `gpu_uuid`, `gpu_index`.
- `sus_gpu_nvml_power_watts`, gauge, labels `gpu_uuid`, `gpu_index`.
- `sus_pin_min_power_watts`, gauge, labels `gpu_uuid`, `gpu_index`.
- `sus_pin_max_power_watts`, gauge, labels `gpu_uuid`, `gpu_index`.
- `sus_pin_balance_ratio`, gauge, labels `gpu_uuid`, `gpu_index`.
- `sus_device_read_success`, gauge, labels `gpu_uuid`, `gpu_index`.
- `sus_scrape_errors_total`, counter, label `reason` with only stable low-cardinality values.

Do not add a `host` label in the exporter. Prometheus already attaches the scrape target as the `instance` label, and operators can add static labels such as `site`, `rack`, or `role` in Prometheus scrape config when needed. Avoid high-cardinality labels. Do not include changing error strings, timestamps, process IDs, PCI details that can vary unexpectedly, or raw command output as labels. Put detailed errors in logs, not Prometheus labels.

## Revision Notes

2026-06-27 / Codex: Created the initial ExecPlan from the agreed direction: narrow the fork into a read-only Prometheus exporter plus one-shot diagnostic tool, with alerting, retention, dashboards, and email handled by the monitoring stack.

2026-06-27 / Codex after adversarial subagent review: Revised the plan to specify NVML lifecycle ownership, partial multi-GPU failure behavior, stable Prometheus error metrics, official Prometheus client usage, loopback-by-default service exposure, systemd hardening, exact hardware-permission diagnostics, NVML index semantics, summary edge cases, idempotent Makefile behavior, 5-second scrape examples, metric validation, race testing, and README handling.

October 5 validation: go test -race ./... and go vet ./... passed. The native exporter runs on both GPU hosts. Central alert rules and Discord delivery are maintained in agents-homelab/monitoring/astral.
