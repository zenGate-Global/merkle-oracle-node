package config

import (
	"math"

	"github.com/Salvionied/apollo/constants"
)

const (
	ToleranceMs  = 300000
	NullTrieHash = "0000000000000000000000000000000000000000000000000000000000000000"
)

type Network struct {
	ShelleyOffsetSlot uint64
	ShelleyOffsetTime int64
	SlotLength        uint64
	NetworkId         constants.Network
}

var Networks = map[string]Network{
	"preview": {
		ShelleyOffsetSlot: 0,
		ShelleyOffsetTime: 1666656000000,
		SlotLength:        1000,
		NetworkId:         constants.PREVIEW,
	},
	"mainnet": {
		ShelleyOffsetSlot: 4924800,
		ShelleyOffsetTime: 1596491091000,
		SlotLength:        1000,
		NetworkId:         constants.MAINNET,
	},
	"custom": {
		ShelleyOffsetSlot: 0,             // zero slot
		ShelleyOffsetTime: 1735520658329, // zero time
		SlotLength:        1000,
		NetworkId:         constants.PREVIEW,
	},
}

func SlotToUnixTime(slot uint64, network Network) int64 {
	msAfterBegin := (slot - network.ShelleyOffsetSlot) * network.SlotLength
	return network.ShelleyOffsetTime + int64(msAfterBegin)
}

func UnixTimeToSlot(unixTime int64, network Network) uint64 {
	timePassed := unixTime - network.ShelleyOffsetTime
	slotPassed := math.Floor(float64(timePassed) / float64(network.SlotLength))
	return uint64(slotPassed) + network.ShelleyOffsetSlot
}
