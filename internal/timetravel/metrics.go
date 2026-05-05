package timetravel

import "github.com/sthalbert/longue-vue/internal/metrics"

// RecordWrite increments the time-travel writes counter.
func RecordWrite(kind, changeType string) {
	metrics.ObserveTimeTravelWrite(kind, changeType)
}

// RecordNoop increments the noop counter (suppressed update — no watched field changed).
func RecordNoop(kind string) {
	metrics.ObserveTimeTravelNoop(kind)
}
