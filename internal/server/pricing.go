package server

import "partners/internal/store"

// Pricing is the amount due for a partnership, as displayed.
type Pricing struct {
	Set    bool
	Amount int
	// Tax is a store.Tax* mode, displayed with the "tax.<mode>" message.
	Tax string
	// Negotiated is true when the partnership overrides the level price.
	Negotiated bool
}

func pricing(p *store.Partnership, l *store.Level) Pricing {
	amount, ok := p.Price(l)

	tax := store.TaxExcluded
	if l != nil && store.ValidTaxMode(l.TaxMode) {
		tax = l.TaxMode
	}

	return Pricing{Set: ok, Amount: amount, Tax: tax, Negotiated: p.PriceOverride != nil}
}
