package provider

import (
	"errors"
	"zenGate-Global/merkle-oracle-node/internal/config"

	connector "github.com/zenGate-Global/cardano-connector-go"
	blockfrostConnector "github.com/zenGate-Global/cardano-connector-go/blockfrost"
	kupmiosConnector "github.com/zenGate-Global/cardano-connector-go/kupmios"
	maestroConnector "github.com/zenGate-Global/cardano-connector-go/maestro"
	utxorpcConnector "github.com/zenGate-Global/cardano-connector-go/utxorpc"
)

func NewProvider(cfg *config.Config) (connector.Provider, error) {

	network := config.Networks[cfg.Network]

	if cfg.Api.BlockfrostURL != "" {
		blockfrostConfig := blockfrostConnector.Config{
			ProjectID: cfg.Api.BlockfrostApiKey,
			BaseURL:   cfg.Api.BlockfrostURL,
			NetworkId: int(network.NetworkId),
		}

		provider, err := blockfrostConnector.New(blockfrostConfig)
		if err != nil {
			return nil, err
		}

		return provider, nil
	} else if cfg.Api.OgmiosURL != "" && cfg.Api.KupoURL != "" {
		kupoConfig := kupmiosConnector.Config{
			OgmigoEndpoint: cfg.Api.OgmiosURL,
			KupoEndpoint:   cfg.Api.KupoURL,
			NetworkId:      int(network.NetworkId),
		}

		provider, err := kupmiosConnector.New(kupoConfig)
		if err != nil {
			return nil, err
		}

		return provider, nil
	} else if cfg.Api.UtxorpcURL != "" {
		utxorpcConfig := utxorpcConnector.Config{
			BaseUrl:   cfg.Api.UtxorpcURL,
			NetworkId: int(network.NetworkId),
		}

		provider, err := utxorpcConnector.New(utxorpcConfig)
		if err != nil {
			return nil, err
		}

		return provider, nil
	} else if cfg.Api.MaestroApiKey != "" {
		maestroConfig := maestroConnector.Config{
			ProjectID:   cfg.Api.MaestroApiKey,
			NetworkName: cfg.Network,
			NetworkId:   int(network.NetworkId),
		}

		provider, err := maestroConnector.New(maestroConfig)
		if err != nil {
			return nil, err
		}

		return provider, nil
	}

	return nil, errors.New("no provider found")
}
