package tx

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"time"
	"zenGate-Global/merkle-oracle-node/internal/config"
	"zenGate-Global/merkle-oracle-node/internal/logging"

	"github.com/Salvionied/apollo/serialization"
	"github.com/Salvionied/apollo/serialization/Address"
	"github.com/Salvionied/apollo/serialization/Redeemer"
	"github.com/Salvionied/apollo/serialization/Transaction"
	"github.com/Salvionied/apollo/serialization/TransactionInput"
	"github.com/Salvionied/apollo/serialization/TransactionOutput"
	"github.com/Salvionied/apollo/serialization/UTxO"
	"github.com/Salvionied/apollo/txBuilding/Backend/Base"
	connector "github.com/zenGate-Global/cardano-connector-go"

	"github.com/allegro/bigcache/v3"
	"github.com/eko/gocache/lib/v4/cache"
	"github.com/eko/gocache/lib/v4/store"
	bigcache_store "github.com/eko/gocache/store/bigcache/v4"
)

var _ Base.ChainContext = (*ConnectorContext)(nil)

type ConnectorContext struct {
	cfg          *config.Config
	provider     connector.Provider
	cacheManager *cache.Cache[[]byte]
	logger       *logging.Logger
}

func NewConnectorContext(
	provider connector.Provider,
	cfg *config.Config,
	logger *logging.Logger,
) (ConnectorContext, error) {

	bigcacheClient, _ := bigcache.New(
		context.Background(),
		bigcache.DefaultConfig(time.Minute*5),
	)
	bigcacheStore := bigcache_store.NewBigcache(bigcacheClient)

	cacheManager := cache.New[[]byte](bigcacheStore)

	return ConnectorContext{
		provider:     provider,
		cfg:          cfg,
		cacheManager: cacheManager,
		logger:       logger,
	}, nil
}

func (b *ConnectorContext) GetContractCbor(scriptHash string) (string, error) {
	scriptBytes, err := b.cacheManager.Get(
		context.Background(),
		fmt.Sprintf("get_contract_cbor:%s", scriptHash),
	)
	if err == nil {
		return hex.EncodeToString(scriptBytes), nil
	}

	scriptFromProvider, err := b.provider.GetScriptCborByScriptHash(
		context.Background(),
		scriptHash,
	)
	if err != nil {
		return "", err
	}

	scriptBytes, err = hex.DecodeString(scriptFromProvider)
	if err != nil {
		return "", fmt.Errorf("failed to decode CBOR hex string: %w", err)
	}

	if err := b.cacheManager.Set(
		context.Background(),
		fmt.Sprintf("get_contract_cbor:%s", scriptHash),
		scriptBytes,
		store.WithExpiration(time.Hour*24),
	); err != nil {
		fmt.Printf(
			"Error caching contract CBOR for key get_contract_cbor:%s: %v\n",
			scriptHash,
			err,
		)
	}

	return scriptFromProvider, nil
}

func (b *ConnectorContext) Network() int {
	return b.provider.Network()
}

func (b *ConnectorContext) GetGenesisParams() (Base.GenesisParameters, error) {
	genesisParamsJson, err := b.cacheManager.Get(
		context.Background(),
		"get_genesis_params",
	)
	if err == nil {
		var genesisParams Base.GenesisParameters
		if unmarshalErr := json.Unmarshal(genesisParamsJson, &genesisParams); unmarshalErr == nil {
			return genesisParams, nil
		}
	}

	genesisParams, err := b.provider.GetGenesisParams(context.Background())
	if err != nil {
		return Base.GenesisParameters{}, err
	}

	if genesisParamsJsonBytes, err := json.Marshal(genesisParams); err == nil {
		if err := b.cacheManager.Set(
			context.Background(),
			"get_genesis_params",
			genesisParamsJsonBytes,
			store.WithExpiration(time.Hour*24),
		); err != nil {
			fmt.Printf("Error caching genesis params: %v\n", err)
		}
	}

	return genesisParams, nil
}

func (b *ConnectorContext) GetProtocolParams() (Base.ProtocolParameters, error) {
	protocolParamsJson, err := b.cacheManager.Get(
		context.Background(),
		"get_protocol_params",
	)
	if err == nil {
		var protocolParams Base.ProtocolParameters
		if unmarshalErr := json.Unmarshal(protocolParamsJson, &protocolParams); unmarshalErr == nil {
			return protocolParams, nil
		}
	}
	protocolParams, err := b.provider.GetProtocolParameters(
		context.Background(),
	)
	if err != nil {
		return Base.ProtocolParameters{}, err
	}

	if protocolParamsJsonBytes, err := json.Marshal(protocolParams); err == nil {
		if err := b.cacheManager.Set(
			context.Background(),
			"get_protocol_params",
			protocolParamsJsonBytes,
			store.WithExpiration(time.Hour*24),
		); err != nil {
			fmt.Printf("Error caching protocol params: %v\n", err)
		}
	}

	return protocolParams, nil
}

func (b *ConnectorContext) GetUtxoFromRef(
	txHash string,
	txIndex int,
) (*UTxO.UTxO, error) {
	cacheKey := fmt.Sprintf("utxo_from_ref:%s:%d", txHash, txIndex)
	utxoBytes, err := b.cacheManager.Get(context.Background(), cacheKey)
	if err == nil {
		var utxo UTxO.UTxO

		txHashBytes, err := hex.DecodeString(txHash)
		if err != nil {
			return nil, err
		}

		utxo.Input = TransactionInput.TransactionInput{
			TransactionId: txHashBytes,
			Index:         txIndex,
		}
		utxo.Output = TransactionOutput.TransactionOutput{}
		err = utxo.Output.UnmarshalCBOR(utxoBytes)
		if err != nil {
			return nil, err
		}
		return &utxo, nil
	}

	utxos, err := b.provider.GetUtxosByOutRef(
		context.Background(),
		[]connector.OutRef{
			{
				TxHash: txHash,
				Index:  uint32(txIndex),
			},
		},
	)
	if err != nil {
		return nil, err
	}
	if len(utxos) == 0 {
		return nil, fmt.Errorf("no %s:%d utxo found", txHash, txIndex)
	}

	if utxoJsonBytes, err := utxos[0].Output.MarshalCBOR(); err == nil {
		if err := b.cacheManager.Set(
			context.Background(),
			cacheKey,
			utxoJsonBytes,
			store.WithExpiration(time.Hour*24),
		); err != nil {
			fmt.Printf(
				"Error caching utxo for key: %s, error: %v\n",
				cacheKey,
				err,
			)
		}
	} else {
		fmt.Printf("Error caching utxo for key: %s, error: %v\n", cacheKey, err)
	}

	return &utxos[0], nil
}

func (b *ConnectorContext) EvaluateTx(
	txBytes []byte,
	additionalUtxos []UTxO.UTxO,
) (map[string]Redeemer.ExecutionUnits, error) {
	eval, err := b.provider.EvaluateTx(
		context.Background(),
		txBytes,
		additionalUtxos,
	)
	if err != nil {
		b.logger.Errorf("error evaluating tx: %v", err)
		b.logger.Errorf("txBytes: %x", txBytes)
		return nil, err
	}

	for key, units := range eval {
		// Add a 2% buffer using integer math with ceiling: x + ceil(x/50)
		units.Mem = units.Mem + (units.Mem+49)/50
		units.Steps = units.Steps + (units.Steps+49)/50
		eval[key] = units
	}

	return eval, nil
}

func (b *ConnectorContext) Epoch() (int, error) {
	epochStr, err := b.cacheManager.Get(context.Background(), "get_epoch")
	if err == nil {
		if epoch, err := strconv.Atoi(string(epochStr)); err == nil {
			return epoch, nil
		}
	}

	epoch, err := b.provider.Epoch(context.Background())
	if err != nil {
		return 0, err
	}

	if err := b.cacheManager.Set(
		context.Background(),
		"get_epoch",
		[]byte(strconv.Itoa(epoch)),
	); err != nil {
		fmt.Printf("Error caching epoch: %v\n", err)
	}

	return epoch, nil
}

func (b *ConnectorContext) MaxTxFee() (int, error) {
	protocolParams, err := b.GetProtocolParams()
	if err != nil {
		return 0, err
	}
	maxTxExSteps, _ := strconv.Atoi(protocolParams.MaxTxExSteps)
	maxTxExMem, _ := strconv.Atoi(protocolParams.MaxTxExMem)

	return int(protocolParams.MaxTxSize*protocolParams.MinFeeCoefficient) +
		int(protocolParams.MinFeeConstant) +
		int(maxTxExSteps*int(protocolParams.PriceStep)) +
		int(maxTxExMem*int(protocolParams.PriceMem)), nil
}

func (b *ConnectorContext) LastBlockSlot() (int, error) {
	tip, err := b.provider.GetTip(context.Background())
	if err != nil {
		return 0, err
	}
	return int(tip.Slot), nil
}

func (b *ConnectorContext) Utxos(address Address.Address) ([]UTxO.UTxO, error) {
	return b.provider.GetUtxosByAddress(context.Background(), address.String())
}

func (b *ConnectorContext) SubmitTx(
	tx Transaction.Transaction,
) (serialization.TransactionId, error) {
	txBytes, err := tx.Bytes()
	if err != nil {
		return serialization.TransactionId{Payload: []byte{}}, err
	}
	submitTx, err := b.provider.SubmitTx(context.Background(), txBytes)
	if err != nil {
		return serialization.TransactionId{Payload: []byte{}}, err
	}
	return serialization.TransactionId{Payload: []byte(submitTx)}, nil
}
