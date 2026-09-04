// Package faults holds the tunable latency and error injection that makes this
// service produce telemetry worth looking at.
//
// A service that always answers in 2ms and never fails draws a flat line and
// teaches nothing. The knobs here exist so the p99 panel has a shape, the error
// ratio has a step, and there is something to click an exemplar on.
package faults

import (
	"encoding/json"
	"math/rand"
	"sync"
	"time"
)

// Config is the injection state. Zero value is "healthy".
//
// The duration fields are time.Duration internally and MILLISECONDS on the
// wire. Those are not the same number, and the difference is the whole reason
// this type marshals itself by hand: a time.Duration is an int64 count of
// nanoseconds, so the obvious `json:"base_latency_ms"` tag on a raw Duration
// serialises 10ms as 10000000 under a field name that promises milliseconds.
// The admin POST handler always read milliseconds, so the API accepted one unit
// and reported another.
type Config struct {
	BaseLatency time.Duration `json:"-"`
	TailLatency time.Duration `json:"-"`
	TailPercent float64       `json:"tail_percent"`
	ErrorRate   float64       `json:"error_rate"`
}

// wireConfig is the JSON shape: durations as whole milliseconds, matching what
// POST /admin/inject accepts.
type wireConfig struct {
	BaseLatencyMS int64   `json:"base_latency_ms"`
	TailLatencyMS int64   `json:"tail_latency_ms"`
	TailPercent   float64 `json:"tail_percent"`
	ErrorRate     float64 `json:"error_rate"`
}

// MarshalJSON writes the durations as milliseconds.
func (c Config) MarshalJSON() ([]byte, error) {
	return json.Marshal(wireConfig{
		BaseLatencyMS: c.BaseLatency.Milliseconds(),
		TailLatencyMS: c.TailLatency.Milliseconds(),
		TailPercent:   c.TailPercent,
		ErrorRate:     c.ErrorRate,
	})
}

// UnmarshalJSON reads milliseconds, so the type round-trips through its own
// encoding rather than through two different units.
func (c *Config) UnmarshalJSON(b []byte) error {
	var w wireConfig
	if err := json.Unmarshal(b, &w); err != nil {
		return err
	}
	c.BaseLatency = time.Duration(w.BaseLatencyMS) * time.Millisecond
	c.TailLatency = time.Duration(w.TailLatencyMS) * time.Millisecond
	c.TailPercent = w.TailPercent
	c.ErrorRate = w.ErrorRate
	return nil
}

// Injector is safe for concurrent use: the HTTP admin endpoint writes it while
// request handlers read it.
type Injector struct {
	mu  sync.RWMutex
	cfg Config
}

// NewInjector returns an injector with a default shape that already has a tail.
// A latency distribution with no tail makes a p99 panel indistinguishable from
// a p50 panel, and the exemplar story is specifically about the tail.
func NewInjector() *Injector {
	return &Injector{cfg: Config{
		BaseLatency: 8 * time.Millisecond,
		TailLatency: 400 * time.Millisecond,
		TailPercent: 0.04,
		ErrorRate:   0,
	}}
}

func (i *Injector) Get() Config {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.cfg
}

func (i *Injector) Set(c Config) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.cfg = c
}

// Latency returns how long this particular call should take, and whether it
// landed in the slow tail.
func (i *Injector) Latency() (time.Duration, bool) {
	c := i.Get()
	if c.TailPercent > 0 && rand.Float64() < c.TailPercent { //nolint:gosec // not cryptographic
		// Spread the tail so the histogram has more than one populated bucket
		// up there; a constant tail value produces a single spike.
		jitter := time.Duration(rand.Int63n(int64(c.TailLatency/2) + 1)) //nolint:gosec // not cryptographic
		return c.TailLatency + jitter, true
	}
	jitter := time.Duration(rand.Int63n(int64(c.BaseLatency) + 1)) //nolint:gosec // not cryptographic
	return c.BaseLatency + jitter, false
}

// ShouldFail reports whether this call should return an error.
func (i *Injector) ShouldFail() bool {
	c := i.Get()
	return c.ErrorRate > 0 && rand.Float64() < c.ErrorRate //nolint:gosec // not cryptographic
}
