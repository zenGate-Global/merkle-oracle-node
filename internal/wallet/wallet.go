package wallet

import (
	"errors"
	"os"
	"path"

	"zenGate-Global/merkle-oracle-node/internal/config"
	"zenGate-Global/merkle-oracle-node/internal/logging"

	"golang.org/x/crypto/blake2b"

	"github.com/blinklabs-io/bursa"
)

var globalWallet = &bursa.Wallet{}

func Setup() {
	// Setup wallet
	cfg := config.GetConfig()
	logger := logging.GetLogger()
	mnemonic := cfg.Wallet.Mnemonic
	if mnemonic == "" {
		pwd, err := os.Getwd()
		if err != nil {
			logger.Panic(err.Error())
		}
		seedPath := path.Join(
			pwd,
			"seed.txt",
		)
		// Read seed.txt if it exists
		if data, err := os.ReadFile(seedPath); err == nil {
			logger.Infof("read mnemonic from %s", seedPath)
			mnemonic = string(data)
		} else if errors.Is(err, os.ErrNotExist) {
			mnemonic, err = bursa.NewMnemonic()
			if err != nil {
				logger.Panic(err)
			}
			// Write seed.txt
			// WARNING: this will clobber existing files
			f, err := os.Create(seedPath)
			if err != nil {
				logger.Panic(err)
			}
			l, err := f.WriteString(mnemonic)
			logger.Debugf("wrote %d bytes to seed.txt", l)
			if err != nil {
				if closeErr := f.Close(); closeErr != nil {
					logger.Errorf("Failed to close file after write error: %v", closeErr)
				}
				logger.Panic(err)
			}
			err = f.Close()
			if err != nil {
				logger.Panic(err)
			}
			logger.Infof("wrote generated mnemonic to %s", seedPath)
		} else {
			logger.Panic(err)
		}
	}
	wallet, err := bursa.NewWallet(
		mnemonic,
		cfg.Network,
		"",
		0, 0, 0, 0,
	)
	if err != nil {
		logger.Panic(err)
	}
	globalWallet = wallet
}

func GetWallet() *bursa.Wallet {
	return globalWallet
}

func PaymentKeyHash() []byte {
	rootKey, err := bursa.GetRootKeyFromMnemonic(globalWallet.Mnemonic, "")
	if err != nil {
		panic(err)
	}
	userPkh := bursa.GetPaymentKey(bursa.GetAccountKey(rootKey, 0), 0).
		Public().
		PublicKey()
	tmpHasher, err := blake2b.New(28, nil)
	if err != nil {
		panic(err)
	}
	tmpHasher.Write(userPkh)
	hash := tmpHasher.Sum(nil)
	return hash
}

func UserPkh() []byte {
	rootKey, err := bursa.GetRootKeyFromMnemonic(globalWallet.Mnemonic, "")
	if err != nil {
		panic(err)
	}
	return bursa.GetPaymentKey(bursa.GetAccountKey(rootKey, 0), 0).
		Public().
		PublicKey()
}
