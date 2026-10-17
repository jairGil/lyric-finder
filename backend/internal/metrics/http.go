package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type HTTP struct {
	registry        *prometheus.Registry
	requests        *prometheus.CounterVec
	requestDuration *prometheus.HistogramVec
	inFlight        prometheus.Gauge
}

func NewHTTP() *HTTP {
	registry := prometheus.NewRegistry()
	registry.MustRegister(collectors.NewGoCollector())
	registry.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

	requests := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "lyrics_http_requests_total",
		Help: "Total HTTP requests completed by the Lyrics API.",
	}, []string{"method", "route", "status"})
	requestDuration := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "lyrics_http_request_duration_seconds",
		Help:    "HTTP request duration in seconds.",
		Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
	}, []string{"method", "route"})
	inFlight := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "lyrics_http_requests_in_flight",
		Help: "Current number of HTTP requests being served.",
	})
	registry.MustRegister(requests, requestDuration, inFlight)

	return &HTTP{
		registry:        registry,
		requests:        requests,
		requestDuration: requestDuration,
		inFlight:        inFlight,
	}
}

func (metrics *HTTP) Handler() http.Handler {
	return promhttp.HandlerFor(metrics.registry, promhttp.HandlerOpts{})
}

func (metrics *HTTP) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		startedAt := time.Now()
		metrics.inFlight.Inc()
		defer metrics.inFlight.Dec()
		responseWriter := &responseWriter{ResponseWriter: writer}
		next.ServeHTTP(responseWriter, request)

		status := responseWriter.status
		if status == 0 {
			status = http.StatusOK
		}
		route := request.Pattern
		if route == "" {
			route = "unmatched"
		}
		duration := time.Since(startedAt).Seconds()

		metrics.requests.WithLabelValues(request.Method, route, strconv.Itoa(status)).Inc()
		metrics.requestDuration.WithLabelValues(request.Method, route).Observe(duration)
	})
}

type responseWriter struct {
	http.ResponseWriter
	status int
}

func (writer *responseWriter) Unwrap() http.ResponseWriter {
	return writer.ResponseWriter
}

func (writer *responseWriter) WriteHeader(status int) {
	if writer.status != 0 {
		return
	}
	writer.status = status
	writer.ResponseWriter.WriteHeader(status)
}

func (writer *responseWriter) Write(body []byte) (int, error) {
	if writer.status == 0 {
		writer.WriteHeader(http.StatusOK)
	}
	return writer.ResponseWriter.Write(body)
}
