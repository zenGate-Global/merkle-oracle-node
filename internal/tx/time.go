package tx

import "zenGate-Global/merkle-oracle-node/internal/config"

type timeParameters struct {
	currentSlot     int64
	futureSlot      int64
	currentSlotUnix int64
	futureSlotUnix  int64
	midSlotUnix     int64
}

func calculateTimeParameters(
	cfg *config.Config,
	currentSlot uint64,
	toleranceMs uint64,
) timeParameters {
	currentSlotUnix := config.SlotToUnixTime(
		currentSlot,
		config.Networks[cfg.Network],
	)
	futureSlot := currentSlot + toleranceMs/1000
	futureSlotUnix := config.SlotToUnixTime(
		uint64(futureSlot),
		config.Networks[cfg.Network],
	)
	midSlotUnix := (currentSlotUnix + futureSlotUnix) / 2

	return timeParameters{
		currentSlot:     int64(currentSlot),
		futureSlot:      int64(futureSlot),
		currentSlotUnix: currentSlotUnix,
		futureSlotUnix:  futureSlotUnix,
		midSlotUnix:     midSlotUnix,
	}
}
