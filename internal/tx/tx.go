package tx

import (
	"context"
	"encoding/hex"
	"fmt"
	"zenGate-Global/merkle-oracle-node/internal/config"
	"zenGate-Global/merkle-oracle-node/internal/logging"
	"zenGate-Global/merkle-oracle-node/internal/wallet"

	"github.com/Salvionied/apollo"
	"github.com/Salvionied/apollo/serialization"
	"github.com/Salvionied/apollo/serialization/Key"
	"github.com/Salvionied/apollo/serialization/PlutusData"
	"github.com/Salvionied/apollo/serialization/Redeemer"
	"github.com/Salvionied/apollo/serialization/Transaction"
	"github.com/Salvionied/apollo/serialization/UTxO"
	connector "github.com/zenGate-Global/cardano-connector-go"
)

type InputKey struct {
	TxId  string
	Index int
}

func BuildRecreateTx(
	cfg *config.Config,
	provider connector.Provider,
	validatorUtxo *UTxO.UTxO,
	trieHash []byte,
	ipfsCid []byte,
) (*Transaction.Transaction, error) {
	logger := logging.GetLogger()
	bursa := wallet.GetWallet()

	cc, err := NewConnectorContext(provider, cfg, logger)
	if err != nil {
		return nil, err
	}
	apollob := apollo.New(&cc)
	apollob, err = apollob.
		SetWalletFromBech32(bursa.PaymentAddress).
		SetWalletAsChangeAddress()

	if err != nil {
		return nil, err
	}

	utxos, err := provider.GetUtxosByAddress(
		context.TODO(),
		bursa.PaymentAddress,
	)
	if err != nil {
		return nil, err
	}

	// Decode the validator lock datum from CBOR
	validatorInputDatumCbor, err := validatorUtxo.Output.GetDatum().
		MarshalCBOR()
	if err != nil {
		return nil, err
	}

	merkleOracleDatum, err := DecodeMerkleOracleDatum(
		hex.EncodeToString(validatorInputDatumCbor),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to decode merkle oracle datum: %w", err)
	}

	adminSingletonPolicyId := merkleOracleDatum.AdminSingletonPolicyId
	adminSingletonPolicyIdString := hex.EncodeToString(adminSingletonPolicyId)
	adminSingletonAssetName := merkleOracleDatum.AdminSingletonAssetName
	adminSingletonAssetNameString := hex.EncodeToString(adminSingletonAssetName)

	multiSigUtxo, err := provider.GetUtxoByUnit(
		context.TODO(),
		adminSingletonPolicyIdString+adminSingletonAssetNameString,
	)
	if err != nil {
		return nil, err
	}

	tip, err := provider.GetTip(context.TODO())

	if err != nil {
		return nil, err
	}

	latestSlot := tip.Slot

	timeParameters := calculateTimeParameters(
		cfg,
		latestSlot,
		config.ToleranceMs,
	)

	apollob = apollob.AddLoadedUTxOs(utxos...).
		SetValidityStart(timeParameters.currentSlot).
		SetTtl(timeParameters.futureSlot)

	merkleOracleRedeemer := Redeemer.Redeemer{
		Tag: Redeemer.SPEND,
		// NOTE: these values are estimated
		// ExUnits: Redeemer.ExecutionUnits{
		// 	Mem:   267077,
		// 	Steps: 91999695,
		// },
		Data: PlutusData.PlutusData{
			PlutusDataType: PlutusData.PlutusArray,
			TagNr:          121,
			Value: PlutusData.PlutusIndefArray{
				PlutusData.PlutusData{
					PlutusDataType: PlutusData.PlutusArray,
					TagNr:          122,
					Value:          PlutusData.PlutusDefArray{},
				},
			},
		},
	}

	postMerkleOracleDatum := MerkleOracleDatum{
		AdminSingletonPolicyId:  adminSingletonPolicyId,
		AdminSingletonAssetName: adminSingletonAssetName,
		MerkleRoot:              trieHash,
		IpfsCid:                 ipfsCid,
		CreatedAt:               timeParameters.midSlotUnix,
	}

	postDatum, err := EncodeMerkleOracleDatum(postMerkleOracleDatum)
	if err != nil {
		return nil, err
	}

	// postDatum := PlutusData.PlutusData{
	// 	PlutusDataType: PlutusData.PlutusArray,
	// 	TagNr:          0,
	// 	Value:          encodedDatum,
	// }

	// cbor.NewConstructor(
	// 	0,
	// 	cbor.IndefLengthList{
	// 		adminSingletonPolicyId,
	// 		adminSingletonAssetName,
	// 		trieHash,
	// 		ipfsCid,
	// 		timeParameters.midSlotUnix,
	// 	},
	// ),

	apollob = apollob.
		PayToContract(
			validatorUtxo.Output.GetAddress(),
			postDatum,
			int(validatorUtxo.Output.Lovelace()),
			true,
			apollo.NewUnit(
				cfg.Contract.SingletonPolicyId,
				cfg.Contract.SingletonName,
				1,
			),
		).
		CollectFrom(
			*validatorUtxo,
			merkleOracleRedeemer,
		).AddReferenceInput(
		cfg.Contract.MerkleOracleScriptRef.TxId,
		int(cfg.Contract.MerkleOracleScriptRef.Index),
	).AddReferenceInput(
		hex.EncodeToString(multiSigUtxo.Input.TransactionId),
		multiSigUtxo.Input.Index,
	)

	walletPkh := wallet.PaymentKeyHash()

	var pubKeyHash serialization.PubKeyHash
	copy(pubKeyHash[:], walletPkh)

	apollob = apollob.AddRequiredSigner(pubKeyHash)

	// apollob = apollob.DisableExecutionUnitsEstimation()

	tx, err := apollob.Complete()
	if err != nil {
		return nil, err
	}

	vKeyBytes, err := hex.DecodeString(bursa.PaymentVKey.CborHex)
	if err != nil {
		return nil, err
	}
	sKeyBytes, err := hex.DecodeString(bursa.PaymentSKey.CborHex)
	if err != nil {
		return nil, err
	}
	// Strip off leading 2 bytes as shortcut for CBOR decoding to unwrap bytes
	vKeyBytes = vKeyBytes[2:]
	sKeyBytes = sKeyBytes[2:]
	vkey := Key.VerificationKey{Payload: vKeyBytes}
	skey := Key.SigningKey{Payload: sKeyBytes}
	tx, err = tx.SignWithSkey(vkey, skey)
	if err != nil {
		return nil, err
	}
	txBytes, err := tx.GetTx().Bytes()
	if err != nil {
		return nil, err
	}
	logger.Debugf("TX bytes: %x", txBytes)
	return tx.GetTx(), nil
}
