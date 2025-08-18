package tx

import (
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGenesisDecodeMerkleOracleRedeemer(t *testing.T) {
	redeemerHex := "d8799fd87980ff"
	redeemerBytes, _ := hex.DecodeString(redeemerHex)
	decodedRedeemer, err := DecodeMerkleOracleRedeemer(redeemerBytes)
	if err != nil {
		t.Fatalf("failed to decode redeemer: %v", err)
	}

	assert.Equal(t, decodedRedeemer.Action, GenesisAction{})
}

func TestSingletonWithdrawDecodeMerkleOracleRedeemer(t *testing.T) {
	redeemerHex := "d8799fd87c9f581c7249d37b3b81a9adda427a7f5e2f93e93b5be16d88c1074cccd602e1ffff"
	redeemerBytes, _ := hex.DecodeString(redeemerHex)
	decodedRedeemer, err := DecodeMerkleOracleRedeemer(redeemerBytes)
	if err != nil {
		t.Fatalf("failed to decode redeemer: %v", err)
	}

	verificationKey, _ := hex.DecodeString(
		"7249d37b3b81a9adda427a7f5e2f93e93b5be16d88c1074cccd602e1",
	)

	assert.Equal(
		t,
		decodedRedeemer.Action,
		SingletonWithdrawAction{VerificationKey: verificationKey},
	)
}
