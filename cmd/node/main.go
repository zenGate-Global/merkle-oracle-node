package main

import (
	"context"
	"encoding/hex"
	"flag"
	"fmt"
	"net/http"
	_ "net/http/pprof" // #nosec G108
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/anthdm/hollywood/actor"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/automaxprocs/maxprocs"
	"go.uber.org/zap"

	"zenGate-Global/merkle-oracle-node/internal/actors"
	"zenGate-Global/merkle-oracle-node/internal/cloud"
	"zenGate-Global/merkle-oracle-node/internal/config"
	"zenGate-Global/merkle-oracle-node/internal/database"
	"zenGate-Global/merkle-oracle-node/internal/logging"
	"zenGate-Global/merkle-oracle-node/internal/oprovider"
	"zenGate-Global/merkle-oracle-node/internal/provider"
	"zenGate-Global/merkle-oracle-node/internal/tx"
	"zenGate-Global/merkle-oracle-node/internal/types"
	"zenGate-Global/merkle-oracle-node/internal/version"
	"zenGate-Global/merkle-oracle-node/internal/wallet"
)

var cmdlineFlags struct {
	configFile string
	debug      bool
}

func main() {
	flag.StringVar(
		&cmdlineFlags.configFile,
		"config",
		"",
		"path to config file to load",
	)
	flag.BoolVar(
		&cmdlineFlags.debug,
		"debug",
		false,
		"enable debug logging",
	)
	flag.Parse()

	cfg, err := config.Load(cmdlineFlags.configFile)
	if err != nil {
		fmt.Printf("Failed to load config: %s\n", err)
		os.Exit(1)
	}

	if err := validateConfig(cfg); err != nil {
		fmt.Printf("Configuration validation failed: %s\n", err)
		os.Exit(1)
	}

	if err := normalizeNames(cfg); err != nil {
		fmt.Printf("Failed to normalize config names: %s\n", err)
		os.Exit(1)
	}

	if err := logging.Setup(cfg); err != nil {
		fmt.Printf("Failed to setup logger: %s\n", err)
		os.Exit(1)
	}
	logger := logging.GetLogger()

	// set log level based on debug flag
	if cmdlineFlags.debug {
		logger = logger.Desugar().
			WithOptions(zap.IncreaseLevel(zap.DebugLevel)).
			Sugar()
	}

	// Configure max processes with our logger wrapper, toss undo func
	zapPrintf := func(format string, args ...interface{}) {
		logger.Infof(format, args...)
	}
	_, err = maxprocs.Set(maxprocs.Logger(zapPrintf))
	if err != nil {
		logger.Errorf("failed to set max processes: %v", err)
		os.Exit(1)
	}

	logger.Infof("merkle oracle node %s started", version.GetVersionString())

	// start debug listener
	if cfg.Debug.ListenPort > 0 {
		logger.Infof(
			"starting debug listener on %s:%d",
			cfg.Debug.ListenAddress,
			cfg.Debug.ListenPort,
		)
		go func() {
			debugger := &http.Server{
				Addr: fmt.Sprintf(
					"%s:%d",
					cfg.Debug.ListenAddress,
					cfg.Debug.ListenPort,
				),
				ReadHeaderTimeout: 60 * time.Second,
			}
			err := debugger.ListenAndServe()
			if err != nil {
				logger.Errorf("failed to start debug listener: %s", err)
				os.Exit(1)
			}
		}()
	}

	// start metrics listener
	if cfg.Metrics.ListenPort > 0 {
		metricsListenAddr := fmt.Sprintf(
			"%s:%d",
			cfg.Metrics.ListenAddress,
			cfg.Metrics.ListenPort,
		)
		logger.Infof(
			"starting listener for prometheus metrics connections on %s",
			metricsListenAddr,
		)
		metricsMux := http.NewServeMux()
		metricsMux.Handle("/metrics", promhttp.Handler())
		metricsSrv := &http.Server{
			Addr:         metricsListenAddr,
			WriteTimeout: 10 * time.Second,
			ReadTimeout:  10 * time.Second,
			Handler:      metricsMux,
		}
		go func() {
			if err := metricsSrv.ListenAndServe(); err != nil {
				logger.Errorf("failed to start metrics listener: %s", err)
				os.Exit(1)
			}
		}()
	}

	wallet.Setup()
	bursa := wallet.GetWallet()
	logger.Infof("loaded mnemonic for address: %s", bursa.PaymentAddress)

	provider, err := provider.NewProvider(cfg)
	if err != nil {
		logger.Errorf("failed to create provider: %v", err)
		os.Exit(1)
	}
	logger.Infof("provider initialized successfully")

	engine, err := actor.NewEngine(actor.NewEngineConfig())
	if err != nil {
		logger.Errorf("failed to create actor engine: %v", err)
		os.Exit(1)
	}

	db, err := database.New(cfg)
	if err != nil {
		logger.Errorf("failed to create database: %v", err)
		os.Exit(1)
	}

	//TODO: make this auto select a provider based on config
	cloudStorage, err := cloud.NewGCSBucket(
		context.Background(),
		cfg.Cloud.BucketName,
		cfg.Cloud.GCPCredentialJSONPath,
	)
	if err != nil {
		logger.Errorf("failed to create cloud storage: %v", err)
		os.Exit(1)
	}

	//TODO: make this auto select a provider based on config
	oracleDataProvider := oprovider.NewHonoProvider(cfg.Oracle.BaseURL)

	cursorPoints, err := db.GetCursorPoints()
	if err != nil {
		logger.Errorf("failed to get cursor points: %v", err)
		os.Exit(1)
	}

	var slot uint64
	var hash string

	if len(cursorPoints) > 0 {
		// this sets slot and hash to the most recent cursor point
		slot = cursorPoints[0].Slot
		hash = hex.EncodeToString(cursorPoints[0].Hash)
	} else {
		slot = cfg.Indexer.InterceptSlot
		hash = cfg.Indexer.InterceptHash
	}

	strategyManagerPID := engine.Spawn(
		actors.NewStrategyManagerActor(
			cfg,
			provider,
			db,
			cloudStorage,
			oracleDataProvider,
		),
		"strategyManager",
		actor.WithID("manager"),
		actor.WithMaxRestarts(5),
		actor.WithRestartDelay(30*time.Second),
	)
	logger.Infow("StrategyManagerActor spawned", "pid", strategyManagerPID)

	go func() {
		time.Sleep(time.Millisecond * 100)

		chainEventCfg := types.StrategyConfig{
			ID:             "chain-event-processor",
			Kind:           "chain-event-processor",
			StartBlockSlot: slot,
			StartBlockHash: hash,
		}
		engine.Send(
			strategyManagerPID,
			actors.LoadStrategyCommand{Config: chainEventCfg},
		)
		logger.Debug("chain event strategy loaded")

	}()

	logger.Infof("offchain executor started successfully on %s", cfg.Network)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigCh

	logger.Infow("received shutdown signal", "signal", sig.String())
	logger.Info("shutting down...")

	// Create a timeout context for shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(
		context.Background(),
		10*time.Second,
	)
	defer shutdownCancel()

	// Shutdown actors with timeout
	done := make(chan struct{})
	go func() {
		defer close(done)
		// Poison actors and wait for them to stop
		strategyManagerDone := engine.Poison(strategyManagerPID)

		// Wait for both to complete
		<-strategyManagerDone.Done()
	}()

	select {
	case <-done:
		logger.Info("shutdown complete")
	case <-shutdownCtx.Done():
		logger.Warn("shutdown timed out, forcing exit")
	}
}

func validateConfig(cfg *config.Config) error {
	hasBlockfrost := cfg.Api.BlockfrostURL != ""
	hasOgmiosKupo := cfg.Api.OgmiosURL != "" && cfg.Api.KupoURL != ""
	hasUtxoRpc := cfg.Api.UtxorpcURL != ""
	hasMaestro := cfg.Api.MaestroApiKey != ""

	if !hasBlockfrost && !hasOgmiosKupo && !hasUtxoRpc && !hasMaestro {
		return fmt.Errorf("at least one provider API must be configured:\n" +
			"  - Blockfrost: set api.blockfrostURL\n" +
			"  - Ogmios+Kupo: set both api.ogmiosURL and api.kupoURL\n" +
			"  - UtxoRPC: set api.utxorpcURL\n" +
			"  - Maestro: set api.maestroApiKey")
	}

	if (cfg.Api.OgmiosURL != "") != (cfg.Api.KupoURL != "") {
		return fmt.Errorf(
			"ogmiosURL and kupoURL must be set together (both or neither)",
		)
	}

	if cfg.Wallet.Mnemonic == "" {
		return fmt.Errorf("wallet.mnemonic is required")
	}

	if cfg.Contract.ContractAddress == "" {
		return fmt.Errorf("contract.contractAddress is required")
	}

	if cfg.Contract.SingletonPolicyId == "" {
		return fmt.Errorf("contract.singletonPolicyId is required")
	}
	if len(cfg.Contract.SingletonPolicyId) != 56 {
		return fmt.Errorf(
			"contract.singletonPolicyId must be exactly 56 characters long, got %d",
			len(cfg.Contract.SingletonPolicyId),
		)
	}
	if cfg.Contract.SingletonName == "" {
		return fmt.Errorf("contract.singletonName is required")
	}

	if cfg.Network == "" {
		return fmt.Errorf("network is required")
	}

	if cfg.Indexer.InterceptHash == "" {
		return fmt.Errorf("indexer.interceptHash is required")
	}
	if cfg.Indexer.InterceptSlot == 0 {
		return fmt.Errorf("indexer.interceptSlot must be greater than 0")
	}

	if cfg.Submit.Url != "" && cfg.Submit.BlockFrostProjectID == "" {
		fmt.Printf(
			"Warning: submit.url is set but submit.blockFrostProjectID is empty. This may cause submission failures if the endpoint requires authentication.\n",
		)
	}

	if hasBlockfrost && cfg.Api.BlockfrostApiKey == "" {
		return fmt.Errorf(
			"api.blockfrostApiKey is required when using Blockfrost provider",
		)
	}

	if cfg.Storage.URL == "" {
		return fmt.Errorf("storage.url cannot be empty")
	}

	return nil
}

func normalizeNames(cfg *config.Config) error {

	originalSingletonName := cfg.Contract.SingletonName
	cfg.Contract.SingletonName = tx.DecodeHexIfValid(cfg.Contract.SingletonName)
	if cfg.Contract.SingletonName != originalSingletonName {
		fmt.Printf("Decoded singleton name from hex: %q -> %q\n",
			originalSingletonName, cfg.Contract.SingletonName)
	}

	return nil
}
