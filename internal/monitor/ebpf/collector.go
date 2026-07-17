package ebpf

import (
	"log/slog"
	"math/rand"
	"sync"
	"time"
)

type MetricSample struct {
	Name      string            `json:"name"`
	Value     float64           `json:"value"`
	Labels    map[string]string `json:"labels,omitempty"`
	Timestamp time.Time         `json:"timestamp"`
	Unit      string            `json:"unit,omitempty"`
}

type MetricHandler func(sample MetricSample)

type Collector struct {
	mu       sync.Mutex
	handlers []MetricHandler
	running  bool
	done     chan struct{}
}

func NewCollector() *Collector {
	return &Collector{
		done: make(chan struct{}),
	}
}

func (c *Collector) Start() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.running {
		return nil
	}

	c.running = true
	slog.Info("eBPF collector started (simulated mode)")

	go c.collectLoop()

	return nil
}

func (c *Collector) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.running {
		return
	}
	c.running = false
	close(c.done)
	slog.Info("eBPF collector stopped")
}

func (c *Collector) OnMetric(handler MetricHandler) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.handlers = append(c.handlers, handler)
}

func (c *Collector) collectLoop() {
	slog.Info("eBPF collector emitting simulated system metrics")

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	hostLabels := map[string]string{"host": "localhost", "source": "ebpf_simulated"}
	netLabels := map[string]string{"host": "localhost", "interface": "eth0", "source": "ebpf_simulated"}
	diskLabels := map[string]string{"host": "localhost", "device": "sda", "source": "ebpf_simulated"}

	for {
		select {
		case <-c.done:
			return
		case now := <-ticker.C:
			c.emit(MetricSample{
				Name:      "cpu_usage_percent",
				Value:     float64(rand.Intn(40) + 10),
				Labels:    hostLabels,
				Timestamp: now,
				Unit:      "percent",
			})

			c.emit(MetricSample{
				Name:      "memory_usage_percent",
				Value:     float64(rand.Intn(30) + 40),
				Labels:    hostLabels,
				Timestamp: now,
				Unit:      "percent",
			})

			c.emit(MetricSample{
				Name:      "tcp_retransmit_count",
				Value:     float64(rand.Intn(5)),
				Labels:    netLabels,
				Timestamp: now,
				Unit:      "count",
			})

			c.emit(MetricSample{
				Name:      "disk_io_latency_ms",
				Value:     float64(rand.Intn(20) + 1),
				Labels:    diskLabels,
				Timestamp: now,
				Unit:      "ms",
			})

			c.emit(MetricSample{
				Name:      "packet_loss_percent",
				Value:     float64(rand.Intn(2)),
				Labels:    netLabels,
				Timestamp: now,
				Unit:      "percent",
			})
		}
	}
}

func (c *Collector) emit(sample MetricSample) {
	for _, h := range c.handlers {
		h(sample)
	}
}
