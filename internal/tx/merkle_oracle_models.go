package tx

import (
	"fmt"

	"github.com/Salvionied/apollo/plutusencoder"
	"github.com/Salvionied/apollo/serialization/PlutusData"
)

type POSIXTime = int64

// pub type MerkleOracleDatum {
// 	admin_singleton_policy_id: PolicyId,
// 	admin_singleton_asset_name: AssetName,
// 	merkle_root: ByteArray,
// 	ipfs_cid: ByteArray,
// 	created_at: Int,
//   }

type MerkleOracleDatum struct {
	_                       struct{} `plutusType:"DefList" plutusConstr:"0"`
	AdminSingletonPolicyId  []byte   `plutusType:"Bytes"`
	AdminSingletonAssetName []byte   `plutusType:"Bytes"`
	MerkleRoot              []byte   `plutusType:"Bytes"`
	IpfsCid                 []byte   `plutusType:"Bytes"`
	CreatedAt               int64    `plutusType:"Int"`
}

// pub type Action {
// 	Genesis
// 	Recreate
// 	ChangeAdmin
// 	SingletonWithdraw
//   }

func DecodeMerkleOracleDatum(hexDatum string) (*MerkleOracleDatum, error) {

	var merkleOracleDatum MerkleOracleDatum
	err := plutusencoder.CborUnmarshal(hexDatum, &merkleOracleDatum, 1)
	if err != nil {
		return nil, fmt.Errorf("failed to decode validator lock datum: %w", err)
	}

	return &merkleOracleDatum, nil
}

func EncodeMerkleOracleDatum(
	merkleOracleDatum MerkleOracleDatum,
) (*PlutusData.PlutusData, error) {
	return plutusencoder.MarshalPlutus(merkleOracleDatum)
}
