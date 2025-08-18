package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// Indexer metrics - tracking blockchain synchronization
	MetricSlot = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "merkle_oracle_node_slot",
		Help: "Merkle oracle node current slot number",
	})
	MetricTipSlot = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "merkle_oracle_node_tip_slot",
		Help: "Slot number for upstream chain tip",
	})
	MetricTipReached = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "merkle_oracle_node_tip_reached",
		Help: "Whether the indexer has reached the chain tip (1 = reached, 0 = syncing)",
	})

	// Strategy metrics - tracking actor system health
	MetricActiveStrategies = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "merkle_oracle_node_active_strategies",
		Help: "Number of currently active strategy actors",
	})
	MetricStrategyRestarts = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "merkle_oracle_node_strategy_restarts_total",
		Help: "Total number of strategy actor restarts",
	}, []string{"strategy_id", "strategy_kind"})

	// Block processing metrics
	MetricBlocksProcessed = promauto.NewCounter(prometheus.CounterOpts{
		Name: "merkle_oracle_node_blocks_processed_total",
		Help: "Total number of blocks processed by the chain event processor",
	})
	MetricTransactionsProcessed = promauto.NewCounter(prometheus.CounterOpts{
		Name: "merkle_oracle_node_transactions_processed_total",
		Help: "Total number of transactions processed by the chain event processor",
	})
	MetricRollbacksProcessed = promauto.NewCounter(prometheus.CounterOpts{
		Name: "merkle_oracle_node_rollbacks_processed_total",
		Help: "Total number of rollback events processed",
	})

	// Merkle oracle node specific metrics
	MetricMerkleOracleNodeEventsProcessed = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "merkle_oracle_node_events_processed_total",
			Help: "Total number of merkle oracle node-related events processed",
		},
	)
	MetricTrieRootMismatches = promauto.NewCounter(prometheus.CounterOpts{
		Name: "merkle_oracle_node_trie_root_mismatches_total",
		Help: "Total number of trie root mismatches detected",
	})

	// Storage metrics
	MetricTrieNodes = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "merkle_oracle_node_trie_nodes",
		Help: "Current number of nodes in the merkle oracle node trie",
	})

	// Actor system metrics
	MetricActorMessages = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "merkle_oracle_node_actor_messages_total",
		Help: "Total number of messages processed by actors",
	}, []string{"actor_type", "message_type"})
	MetricActorRestarts = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "merkle_oracle_node_actor_restarts_total",
		Help: "Total number of actor restarts",
	}, []string{"actor_type", "actor_id"})

	// Error metrics
	MetricProcessingErrors = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "merkle_oracle_node_processing_errors_total",
		Help: "Total number of processing errors",
	}, []string{"component", "error_type"})

	// Performance metrics
	MetricProcessingDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "merkle_oracle_node_processing_duration_seconds",
			Help:    "Time spent processing different types of events",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"component", "operation"},
	)

	// Indexer restart circuit breaker metrics
	MetricIndexerRestarts = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "merkle_oracle_node_indexer_restarts_total",
		Help: "Total number of indexer restarts by type (partial_restart, full_reset)",
	}, []string{"restart_type", "reason"})

	MetricRestartCircuitBreakerActivations = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "merkle_oracle_node_restart_circuit_breaker_activations_total",
			Help: "Total number of times the restart circuit breaker was activated to perform full reset",
		},
	)
)

// Helper functions for common metric operations

// IncrementStrategyCount increments the active strategies counter
func IncrementStrategyCount() {
	MetricActiveStrategies.Inc()
}

// DecrementStrategyCount decrements the active strategies counter
func DecrementStrategyCount() {
	MetricActiveStrategies.Dec()
}

// RecordStrategyRestart records a strategy restart
func RecordStrategyRestart(strategyID, strategyKind string) {
	MetricStrategyRestarts.WithLabelValues(strategyID, strategyKind).Inc()
}

// UpdateSlotMetrics updates both current slot and tip slot metrics
func UpdateSlotMetrics(currentSlot, tipSlot uint64, tipReached bool) {
	MetricSlot.Set(float64(currentSlot))
	MetricTipSlot.Set(float64(tipSlot))
	if tipReached {
		MetricTipReached.Set(1)
	} else {
		MetricTipReached.Set(0)
	}
}

// RecordActorMessage records a message processed by an actor
func RecordActorMessage(actorType, messageType string) {
	MetricActorMessages.WithLabelValues(actorType, messageType).Inc()
}

// RecordActorRestart records an actor restart
func RecordActorRestart(actorType, actorID string) {
	MetricActorRestarts.WithLabelValues(actorType, actorID).Inc()
}

// RecordProcessingError records a processing error
func RecordProcessingError(component, errorType string) {
	MetricProcessingErrors.WithLabelValues(component, errorType).Inc()
}

// RecordProcessingDuration records the duration of a processing operation
func RecordProcessingDuration(component, operation string, duration float64) {
	MetricProcessingDuration.WithLabelValues(component, operation).
		Observe(duration)
}
