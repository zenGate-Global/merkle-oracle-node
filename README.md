# Merkle Oracle Node

[![License: Apache 2.0](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)
[![Go Version](https://img.shields.io/github/go-mod/go-version/zenGate-Global/merkle-oracle-node)](go.mod)
[![CI/CD](https://github.com/zenGate-Global/merkle-oracle-node/actions/workflows/go-test.yml/badge.svg)](https://github.com/zenGate-Global/merkle-oracle-node/actions/workflows/go-test.yml)

The Merkle Oracle Node monitors the blockchain and, at timed intervals defined in the config, fetches the latest oracle data and adds it to a local Merkle trie. It then uploads the data to a cloud provider such as IPFS and submits a `recreate` oracle transaction to the blockchain.

Once the transaction is confirmed, the node will index it to the local database.

Rollbacks are handled by the node.

The node also exposes a comprehensive RESTful API, allowing users and applications to query the current and historical state of the data and retrieve individual data points.

## Architecture Overview

-   **Entrypoint (`cmd/node/main.go`):** Initializes all components, including configuration, logging, database, and the main actor engine.
-   **Actor System (`internal/actors`):** Uses the `anthdm/hollywood` actor framework to manage concurrent processes.
    -   **`IndexerActor`:** Connects to a Cardano node using the `blinklabs-io/adder` library. It streams blocks and transactions, handling rollbacks and forwarding events downstream.
    -   **`StrategyManagerActor`:** Supervises the core logic. It spawns and manages the `IndexerActor` and other processing strategies. It also handles the indexer restart and circuit breaker logic.
    -   **`ChainEventProcessorStrategy`:** The core business logic of the oracle. It listens for blockchain events, determines when to fetch new off-chain data, calculates trie diffs, builds and submits Cardano transactions with the new Merkle root, and persists the new state to the database.
-   **Database (`internal/database`):** Manages all interactions with the PostgreSQL database using `gorm`. It stores oracle files, trie history, objects, keys, and values, and includes functions for state rollback and historical queries.
-   **Cardano Interaction (`internal/provider`, `internal/tx`):**
    -   Uses `zenGate-Global/cardano-connector-go` to abstract interactions with various Cardano data providers (Blockfrost, Kupmios, etc.).
    -   Uses `Salvionied/apollo` for building, balancing, and signing Cardano transactions.
-   **Cloud Storage (`internal/cloud`):** Provides a generic `Cloud` interface with implementations for Google Cloud Storage and IPFS (via Pinata) to store oracle data snapshots.
-   **API Server (`internal/api`):** A `gin-gonic` based web server that provides the public REST API for querying oracle data. It includes auto-generated interactive documentation using `bdpiprava/scalar-go`.

## Architecture Diagram

![Architecture Diagram](./imgs/architecture-diagram.png)

## Getting Started

## Related Repositories

-   [Merkle Oracle Contracts](https://github.com/zenGate-Global/merkle-oracle-contracts)
-   [Merkle Oracle CLI](https://github.com/zenGate-Global/merkle-oracle-cli)

### Prerequisites

-   Go `1.24` or later
-   Docker and Docker Compose
-   Access to a running PostgreSQL instance (can use dockerized postgres)

### Configuration

The node is configured using a `config.yaml` file. An example with all available options is provided in `config.example.yaml`.

1.  Copy the example configuration file:
    ```bash
    cp config.example.yaml config.yaml
    ```
2.  Edit `config.yaml` and fill in the required values. See the detailed breakdown below.

#### `config.yaml` Breakdown

-   **`storage`**:
    -   `url`: (string) Your PostgreSQL connection string. Ex: `postgresql://postgres:postgres@localhost:5432/postgres`
-   **`indexer`**:
    -   `address`: (string) The TCP address of your Cardano node (e.g., `IP_ADDR:3001`).
    -   `socketPath`: (string) Filesystem path to your Cardano node's IPC socket. **Note:** Use either `address` or `socketPath`.
    -   `interceptHash` & `interceptSlot`: (string, int) The block hash and slot to start indexing from if the database is empty.
    -   `restartThreshold` & `restartTimeWindow`: (int, duration) Configures the indexer's circuit breaker. If the indexer restarts more than `restartThreshold` times within `restartTimeWindow`, it performs a full reset to the intercept point.
-   **`metrics`**, **`debug`**, **`server`**:
    -   `listenAddress` & `listenPort`: The address and port for the Prometheus metrics, Go pprof debugger, and public API server, respectively.
-   **`submit`**:
    -   `url`: (string) A custom URL for submitting transactions (e.g., a Blockfrost submit endpoint). If empty, the provider from the `api` section will be used.
    -   `blockFrostProjectID`: (string) Your Blockfrost Project ID, required if using the Blockfrost submit endpoint.
-   **`wallet`**:
    -   `mnemonic`: (string) The 24-word seed phrase of the wallet used to sign and pay for transactions. **CRITICAL: Secure this value.**
-   **`logging`**:
    -   `level`: (string) Log level (`debug`, `info`, `warn`, `error`).
    -   `log_discord_webook_url`, `notification_discord_webhook_url`: Optional Discord webhooks for logging and notifications.
    -   Other fields control log file rotation.
-   **`api`**:
    -   Configure **at least one** Cardano provider (Blockfrost, Ogmios+Kupo, UtxoRPC, or Maestro) to enable the node to query the blockchain.
-   **`cloud`**:
    -   Configure **one** cloud provider for storing data snapshots.
    -   **GCP**: `gcpCredentialJSONPath`, `bucketName`.
    -   **IPFS**: `pinataGatewayURL`, `pinataJWT`.
-   **`oracle`**:
    -   `updateInterval`: (duration) The minimum time that must pass before the oracle will publish a new root on-chain. Ex: `10m`.
    -   `baseURL`: (string) The base URL of the off-chain data source API, check out the interface in `internal/oprovider/oprovider.go` to determine the format for the data provider integration.
-   **`network`**: (string) The Cardano network to connect to (`mainnet`, `preview`, etc.).
-   **`contract`**:
    -   `contractAddress`: (string) The on-chain address of the Merkle oracle validator script.
    -   `singletonPolicyId` & `singletonName`: (string) The policy ID and asset name of the NFT that ensures the contract's UTxO is unique.
    -   `merkleOracleScriptRef`: The transaction output reference (`txId`, `index`) that contains the validator script.

Note: the `contract` section values are determined after running the `genesis` command with the [Merkle Oracle CLI](https://github.com/zenGate-Global/merkle-oracle-cli).

### Running with Docker Compose (Recommended)

1.  **Configure:** Create and edit your `config.yaml` as described above.

2.  **Prepare Volumes:** Run the provided setup script. This script creates the required Docker volume and sets the correct file ownership for `config.yaml` and the log directory, which is necessary as the container runs with a non-root user for enhanced security.
    ```bash
    bash ./setup_docker_compose.sh
    ```

3.  **Start Services:** Launch the oracle node and the PostgreSQL database.
    ```bash
    docker compose up -d
    ```

4.  **Monitor Logs:**
    ```bash
    docker compose logs -f merkle-oracle-node
    ```

5.  **Stop Services:**
    ```bash
    docker compose down
    ```

### Running from Source

1.  Ensure you have a running PostgreSQL instance.
2.  Configure your `config.yaml` with the database connection string and other parameters.
3.  Install dependencies:
    ```bash
    go mod tidy
    ```
4.  Run the application:
    ```bash
    go run ./cmd/node -config ./config.yaml
    ```
    Alternatively, build a binary first using the `Makefile`:
    ```bash
    make build
    ./node -config ./config.yaml
    ```

    A convinience script is provided to run the node as well.
    ```bash
    ./run.sh
    ```

## API Documentation

The node provides swagger documentation. Once the node is running, you can access it at:

Make sure the port matches the `server.listenPort` in the config.

**http://localhost:8080/docs**

### Logging

The service generates structured JSON logs to both the standard output and a rotating log file located in `./assets/logs/`. Logging behavior (level, file size, rotation) is configurable in `config.yaml`.

### Metrics

Prometheus-compatible metrics are exposed on the `/metrics` endpoint (default port `9094`). Key metrics include:
-   `merkle_oracle_node_slot`: The current slot being processed by the indexer.
-   `merkle_oracle_node_tip_reached`: A gauge (1 or 0) indicating if the indexer is synced to the chain tip.
-   `merkle_oracle_node_blocks_processed_total`: A counter for the total number of processed blocks.
-   `merkle_oracle_node_rollbacks_processed_total`: A counter for the total number of rollbacks handled.
-   `merkle_oracle_node_trie_root_mismatches_total`: A counter for detected mismatches between the local and on-chain Merkle root.
-   `merkle_oracle_node_indexer_restarts_total`: A counter for indexer restarts, labeled by reason.

## Development

The project includes a `Makefile` to streamline common development tasks.

-   `make build`: Compiles the node binary into the root directory.
-   `make test`: Runs all Go tests in the project.
-   `make format`: Formats all Go source code using `gofmt` and `golines`.
-   `make lint`: Runs `golangci-lint` to check for code quality and style issues.
-   `make lint-fix`: Runs the linter with the `--fix` flag to automatically correct issues.
-   `make gen-docs`: Auto generates the API documentation for swagger, make sure to rebuild the binary after running this.

The project enforces [Conventional Commits](https://www.conventionalcommits.org/) via a GitHub Action on pull requests.