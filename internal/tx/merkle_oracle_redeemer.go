package tx

import (
	"fmt"

	"github.com/blinklabs-io/gouroboros/cbor"
)

type Action interface {
	actionType() int
	MarshalCBOR() ([]byte, error)
	String() string
}

type MerkleOracleRedeemer struct {
	cbor.StructAsArray
	Action Action
}

func (t *MerkleOracleRedeemer) MarshalCBOR() ([]byte, error) {
	tmp := cbor.NewConstructor(
		0,
		cbor.IndefLengthList{
			t.Action,
		},
	)
	return cbor.Encode(&tmp)
}

func (t *MerkleOracleRedeemer) UnmarshalCBOR(cborData []byte) error {
	var tmpConstr cbor.Constructor
	if _, err := cbor.Decode(cborData, &tmpConstr); err != nil {
		return err
	}

	fields := tmpConstr.Fields()
	if len(fields) != 1 {
		return fmt.Errorf("expected 1 field, got %d", len(fields))
	}

	actionConstr, ok := fields[0].(cbor.Constructor)
	if !ok {
		return fmt.Errorf("expected constructor for Action, got %T", fields[0])
	}
	action, err := ParseAction(
		actionConstr.Constructor(),
		actionConstr.Fields(),
	)
	if err != nil {
		return fmt.Errorf("failed to parse Action: %v", err)
	}
	t.Action = action

	return nil
}

// Helper function to create an Action from a constructor and fields
func ParseAction(constructor uint, fields []interface{}) (Action, error) {
	switch constructor {
	case 0:
		return GenesisAction{}, nil
	case 1:
		return RecreateAction{}, nil
	case 2:
		return ChangeAdminAction{}, nil
	case 3:
		if len(fields) != 1 {
			return nil, fmt.Errorf(
				"singleton withdraw action requires 1 field, got %d",
				len(fields),
			)
		}
		// Handle both []byte and cbor.ByteString types
		var verificationKey []byte
		switch v := fields[0].(type) {
		case []byte:
			verificationKey = v
		case cbor.ByteString:
			verificationKey = v.Bytes()
		default:
			return nil, fmt.Errorf("expected []byte or cbor.ByteString, got %T", fields[0])
		}
		return SingletonWithdrawAction{VerificationKey: verificationKey}, nil
	default:
		return nil, fmt.Errorf("unknown action constructor: %d", constructor)
	}
}

type GenesisAction struct{}

func (a GenesisAction) actionType() int { return 0 }
func (a GenesisAction) String() string  { return "Genesis" }
func (a GenesisAction) MarshalCBOR() ([]byte, error) {
	constructor := cbor.NewConstructor(0, []interface{}{})
	return cbor.Encode(constructor)
}

type RecreateAction struct{}

func (a RecreateAction) actionType() int { return 1 }
func (a RecreateAction) String() string  { return "Recreate" }
func (a RecreateAction) MarshalCBOR() ([]byte, error) {
	constructor := cbor.NewConstructor(1, []interface{}{})
	return cbor.Encode(constructor)
}

type ChangeAdminAction struct{}

func (a ChangeAdminAction) actionType() int { return 2 }
func (a ChangeAdminAction) String() string  { return "ChangeAdmin" }
func (a ChangeAdminAction) MarshalCBOR() ([]byte, error) {
	constructor := cbor.NewConstructor(2, []interface{}{})
	return cbor.Encode(constructor)
}

type SingletonWithdrawAction struct {
	VerificationKey []byte
}

func (a SingletonWithdrawAction) actionType() int { return 3 }
func (a SingletonWithdrawAction) String() string  { return "SingletonWithdraw" }
func (a SingletonWithdrawAction) MarshalCBOR() ([]byte, error) {
	constructor := cbor.NewConstructor(
		3,
		cbor.IndefLengthList{a.VerificationKey},
	)
	return cbor.Encode(constructor)
}

func DecodeMerkleOracleRedeemer(
	hexDatum []byte,
) (*MerkleOracleRedeemer, error) {

	datum := &MerkleOracleRedeemer{}
	if _, err := cbor.Decode(hexDatum, datum); err != nil {
		return nil, fmt.Errorf(
			"failed to decode merkle oracle redeemer: %w",
			err,
		)
	}

	return datum, nil
}
