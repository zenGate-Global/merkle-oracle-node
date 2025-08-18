package actors

import (
	"zenGate-Global/merkle-oracle-node/internal/cloud"
	"zenGate-Global/merkle-oracle-node/internal/config"
	"zenGate-Global/merkle-oracle-node/internal/database"
	"zenGate-Global/merkle-oracle-node/internal/oprovider"
	"zenGate-Global/merkle-oracle-node/internal/strategy"
	"zenGate-Global/merkle-oracle-node/internal/types"

	"github.com/anthdm/hollywood/actor"
	connector "github.com/zenGate-Global/cardano-connector-go"
)

// StrategyRegistry holds factory functions for creating strategy actors
type StrategyRegistry struct {
	factories map[string]func(types.StrategyConfig, *config.Config, connector.Provider) actor.Producer
}

// NewStrategyRegistry creates a new strategy registry with all available strategies
func NewStrategyRegistry(
	db *database.Database,
	cloud cloud.Cloud,
	oracleDataProvider oprovider.Provider,
) *StrategyRegistry {
	registry := &StrategyRegistry{
		factories: make(
			map[string]func(types.StrategyConfig, *config.Config, connector.Provider) actor.Producer,
		),
	}

	registry.factories["chain-event-processor"] = func(cfg types.StrategyConfig, appCfg *config.Config, provider connector.Provider) actor.Producer {
		return strategy.NewChainEventProcessorStrategy(
			cfg,
			appCfg,
			provider,
			db,
			cloud,
			oracleDataProvider,
		)
	}

	return registry
}

// CreateStrategy creates a strategy actor producer for the given configuration
func (r *StrategyRegistry) CreateStrategy(
	cfg types.StrategyConfig,
	appCfg *config.Config,
	provider connector.Provider,
) (actor.Producer, bool) {
	factory, exists := r.factories[cfg.Kind]
	if !exists {
		return nil, false
	}
	return factory(cfg, appCfg, provider), true
}

func (r *StrategyRegistry) GetAvailableStrategies() []string {
	strategies := make([]string, 0, len(r.factories))
	for kind := range r.factories {
		strategies = append(strategies, kind)
	}
	return strategies
}
