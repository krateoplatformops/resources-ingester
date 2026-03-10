package router

import "github.com/prometheus/client_golang/prometheus"

type WorkerPoolMetrics struct {
	processed prometheus.Counter
	errors    prometheus.Counter
	duration  prometheus.Histogram
}

func NewWorkerPoolMetrics(reg prometheus.Registerer) *WorkerPoolMetrics {
	m := &WorkerPoolMetrics{
		processed: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "workerpool_events_processed_total",
			Help: "Total number of events successfully processed.",
		}),
		errors: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "workerpool_events_errors_total",
			Help: "Total number of events that failed processing.",
		}),
		duration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name: "workerpool_event_duration_seconds",
			Help: "Processing time per event in seconds.",
			// Buckets tuned for typical controller reconcile latency.
			// Adjust to your workload: if p99 is routinely >10s, extend the range.
			Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
		}),
	}
	reg.MustRegister(m.processed, m.errors, m.duration)
	return m
}
