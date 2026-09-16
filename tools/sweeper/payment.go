// Copyright (C) 2026 Storj Labs, Inc.
// See LICENSE for copying information.
package sweeper

// PaymentType selects which kind of payment a single execution works on. One
// execution handles exactly one payment type: each type settles on its own
// chain and needs its own transactions, signed for that chain ID, so L1 and L2
// are swept by separate invocations.
type PaymentType string

const (
	// PaymentTypeL1 is a payment on Ethereum, the layer 1 chain.
	PaymentTypeL1 PaymentType = "l1"
	// PaymentTypeL2 is a payment on zkSync Era, the layer 2 chain.
	PaymentTypeL2 PaymentType = "l2"
	// PaymentTypeL17702 is a payment on Ethereum swept through an EIP-7702
	// delegation instead of transactions sent by the deposit wallet itself.
	// See Delegation for what that changes.
	PaymentTypeL17702 PaymentType = "l1-7702"
)

// Network returns the name of the chain this payment type settles on, as used
// in logs and reports.
func (p PaymentType) Network() string {
	switch p {
	case PaymentTypeL1, PaymentTypeL17702:
		return "ethereum"
	case PaymentTypeL2:
		return "zksync"
	default:
		return string(p)
	}
}
