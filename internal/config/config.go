package config

import (
	"fmt"
	"os"
	"time"

	ouroboros "github.com/blinklabs-io/gouroboros"
	"github.com/kelseyhightower/envconfig"
	"gopkg.in/yaml.v2"
)

type Config struct {
	Storage      StorageConfig  `yaml:"storage"`
	Indexer      IndexerConfig  `yaml:"indexer"`
	Submit       SubmitConfig   `yaml:"submit"`
	Wallet       WalletConfig   `yaml:"wallet"`
	Contract     ContractConfig `yaml:"contract"`
	Api          ApiConfig      `yaml:"api"`
	Cloud        CloudConfig    `yaml:"cloud"`
	Oracle       OracleConfig   `yaml:"oracle"`
	Logging      Logging        `yaml:"logging"`
	Debug        DebugConfig    `yaml:"debug"`
	Metrics      MetricsConfig  `yaml:"metrics"`
	Server       ServerConfig   `yaml:"server"`
	Profile      string         `yaml:"profile"  envconfig:"PROFILE"`
	Network      string         `yaml:"network"  envconfig:"NETWORK"`
	NetworkMagic uint32
}

type ServerConfig struct {
	ListenAddress    string `yaml:"listenAddress"`
	ListenPort       int    `yaml:"listenPort"`
	EnableDebug      bool   `yaml:"enableDebug"`
	DefaultPageLimit int    `yaml:"defaultPageLimit" envconfig:"SERVER_DEFAULT_PAGE_LIMIT"`
	MaxPageLimit     int    `yaml:"maxPageLimit"     envconfig:"SERVER_MAX_PAGE_LIMIT"`
}

type IndexerConfig struct {
	Address                 string `yaml:"address"                 envconfig:"INDEXER_TCP_ADDRESS"`
	SocketPath              string `yaml:"socketPath"              envconfig:"INDEXER_SOCKET_PATH"`
	InterceptHash           string `yaml:"interceptHash"           envconfig:"INDEXER_INTERCEPT_HASH"`
	InterceptSlot           uint64 `yaml:"interceptSlot"           envconfig:"INDEXER_INTERCEPT_SLOT"`
	TxExpirationBlockNumber uint64 `yaml:"txExpirationBlockNumber" envconfig:"INDEXER_TX_EXPIRATION_BLOCK_NUMBER"`

	// Restart tracking configuration
	RestartThreshold  int           `yaml:"restartThreshold"  envconfig:"INDEXER_RESTART_THRESHOLD"`
	RestartTimeWindow time.Duration `yaml:"restartTimeWindow" envconfig:"INDEXER_RESTART_TIME_WINDOW"`
}

type SubmitConfig struct {
	Url                 string `yaml:"url"                 envconfig:"SUBMIT_URL"`
	BlockFrostProjectID string `yaml:"blockFrostProjectID" envconfig:"SUBMIT_BLOCKFROST_PROJECT_ID"`
}

type StorageConfig struct {
	URL string `yaml:"url" envconfig:"DATABASE_URL"`
}

type WalletConfig struct {
	Mnemonic string `yaml:"mnemonic" envconfig:"MNEMONIC"`
}

type ApiConfig struct {
	BlockfrostURL    string `yaml:"blockfrostURL"    envconfig:"API_BLOCKFROST_URL"`
	BlockfrostApiKey string `yaml:"blockfrostApiKey" envconfig:"BLOCKFROST_API_KEY"`
	OgmiosURL        string `yaml:"ogmiosURL"        envconfig:"OGMIOS_URL"`
	KupoURL          string `yaml:"kupoURL"          envconfig:"KUPO_URL"`
	UtxorpcURL       string `yaml:"utxorpcURL"       envconfig:"UTXORPC_URL"`
	MaestroApiKey    string `yaml:"maestroApiKey"    envconfig:"MAESTRO_API_KEY"`
}

type CloudConfig struct {
	GCPCredentialJSONPath string `yaml:"gcpCredentialJSONPath" envconfig:"GCP_CREDENTIAL_JSON_PATH"`
	BucketName            string `yaml:"bucketName"            envconfig:"BUCKET_NAME"`
	PinataGatewayURL      string `yaml:"pinataGatewayURL"      envconfig:"PINATA_GATEWAY_URL"`
	PinataJWT             string `yaml:"pinataJWT"             envconfig:"PINATA_JWT"`
}

type Logging struct {
	Level                         string `yaml:"level"`
	LogDiscordWebookURL           string `yaml:"log_discord_webook_url"`
	NotificationDiscordWebhookURL string `yaml:"notification_discord_webhook_url"`
	ExplorerBaseURI               string `yaml:"explorer_base_uri"`
	LogFileName                   string `yaml:"log_file_name"`
	LogDirectory                  string `yaml:"log_directory"`
	MaxLogFileSize                int    `yaml:"max_log_file_size"`
	MaxBackups                    int    `yaml:"max_backups"`
	MaxAge                        int    `yaml:"max_age"`
}

type DebugConfig struct {
	ListenAddress string `yaml:"listenAddress" envconfig:"DEBUG_LISTEN_ADDRESS"`
	ListenPort    int    `yaml:"listenPort"    envconfig:"DEBUG_LISTEN_PORT"`
}

type MetricsConfig struct {
	ListenAddress string `yaml:"listenAddress" envconfig:"METRICS_LISTEN_ADDRESS"`
	ListenPort    int    `yaml:"listenPort"    envconfig:"METRICS_LISTEN_PORT"`
}

type OracleConfig struct {
	UpdateInterval time.Duration `yaml:"updateInterval" envconfig:"ORACLE_UPDATE_INTERVAL"`
	BaseURL        string        `yaml:"baseURL"        envconfig:"ORACLE_BASE_URL"`
}

type ContractConfig struct {
	ContractAddress       string    `yaml:"contractAddress"       envconfig:"CONTRACT_ADDRESS"`
	SingletonPolicyId     string    `yaml:"singletonPolicyId"     envconfig:"SINGLETON_POLICY_ID"`
	SingletonName         string    `yaml:"singletonName"         envconfig:"SINGLETON_NAME"`
	MerkleOracleScriptRef OutputRef `yaml:"merkleOracleScriptRef" envconfig:"MERKLE_ORACLE_SCRIPT_REF"`
}

type OutputRef struct {
	TxId  string `yaml:"txId"`
	Index uint64 `yaml:"index"`
}

// Singleton config instance with default values
var globalConfig = &Config{}

func (c *Config) setDefaults() {

	network := "mainnet"

	// Indexer defaults
	if c.Indexer.TxExpirationBlockNumber == 0 {
		c.Indexer.TxExpirationBlockNumber = 3
	}

	// Restart tracking defaults
	if c.Indexer.RestartThreshold == 0 {
		c.Indexer.RestartThreshold = 3
	}
	if c.Indexer.RestartTimeWindow == 0 {
		c.Indexer.RestartTimeWindow = 10 * time.Minute
	}

	if c.Logging.Level == "" {
		c.Logging.Level = "info"
	}
	if c.Logging.LogFileName == "" {
		c.Logging.LogFileName = network + ".log"
	}
	if c.Logging.LogDirectory == "" {
		c.Logging.LogDirectory = "assets/logs"
	}
	if c.Logging.MaxLogFileSize == 0 {
		c.Logging.MaxLogFileSize = 100 // 100 MB
	}
	if c.Logging.MaxBackups == 0 {
		c.Logging.MaxBackups = 3
	}
	if c.Logging.MaxAge == 0 {
		c.Logging.MaxAge = 30 // 30 days
	}
	if c.Logging.ExplorerBaseURI == "" {
		c.Logging.ExplorerBaseURI = "https://cardanoscan.io/transaction/"
	}

	// Debug defaults
	if c.Debug.ListenAddress == "" {
		c.Debug.ListenAddress = "localhost"
	}
	// Debug port defaults to 0 (disabled) unless explicitly set

	// Metrics defaults
	if c.Metrics.ListenAddress == "" {
		c.Metrics.ListenAddress = "localhost"
	}
	// Metrics port defaults to 0 (disabled) unless explicitly set

	// Server defaults
	if c.Server.ListenAddress == "" {
		c.Server.ListenAddress = "localhost"
	}
	if c.Server.ListenPort == 0 {
		c.Server.ListenPort = 8080
	}

	if c.Server.DefaultPageLimit == 0 {
		c.Server.DefaultPageLimit = 50
	}
	if c.Server.MaxPageLimit == 0 {
		c.Server.MaxPageLimit = 1000
	}

	// Network defaults
	if c.Network == "" {
		c.Network = network
	}
	if c.Profile == "" {
		c.Profile = "merkle-oracle-node-" + network
	}
}

func Load(configFile string) (*Config, error) {
	// Set defaults first
	globalConfig.setDefaults()

	// Load config file as YAML if provided
	if configFile != "" {
		buf, err := os.ReadFile(configFile)
		if err != nil {
			return nil, fmt.Errorf("error reading config file: %s", err)
		}
		err = yaml.Unmarshal(buf, globalConfig)
		if err != nil {
			return nil, fmt.Errorf("error parsing config file: %s", err)
		}
	}

	// Apply defaults again after loading config file to fill in any missing values
	globalConfig.setDefaults()

	// Load config values from environment variables
	// We use "dummy" as the app name here to (mostly) prevent picking up env
	// vars that we hadn't explicitly specified in annotations above
	err := envconfig.Process("dummy", globalConfig)
	if err != nil {
		return nil, fmt.Errorf("error processing environment: %s", err)
	}
	// Populate network magic value
	if err := globalConfig.populateNetworkMagic(); err != nil {
		return nil, err
	}

	return globalConfig, nil
}

// GetConfig returns the global config instance
func GetConfig() *Config {
	return globalConfig
}

func (c *Config) populateNetworkMagic() error {
	network, exists := ouroboros.NetworkByName(c.Network)
	if !exists {
		return fmt.Errorf("unknown network: %s", c.Network)
	}
	c.NetworkMagic = network.NetworkMagic
	return nil
}
