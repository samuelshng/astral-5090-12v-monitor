DEPS = sus.go go.mod go.sum

.PHONY: all
all: bin/susm bin/susd bin/sus-exporter bin/sus-check

.PHONY: clean
clean:
	rm -rf bin

bin/susm: susm/main.go $(DEPS) | bin
	go build -o $@ ./susm

bin/susd: susd/main.go $(DEPS) | bin
	go build -o $@ ./susd

bin/sus-exporter: cmd/sus-exporter/main.go $(DEPS) | bin
	go build -o $@ ./cmd/sus-exporter

bin/sus-check: cmd/sus-check/main.go $(DEPS) | bin
	go build -o $@ ./cmd/sus-check

bin:
	mkdir -p bin
