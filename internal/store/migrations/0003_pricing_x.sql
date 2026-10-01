ALTER TABLE organizations ADD COLUMN x TEXT NOT NULL DEFAULT '';
-- tax_mode: 'ht' (price excluding VAT), 'ttc' (price including VAT) or
-- 'none' (not subject to VAT).
ALTER TABLE levels ADD COLUMN tax_mode TEXT NOT NULL DEFAULT 'ht';
-- price_override is a negotiated price replacing the level price.
ALTER TABLE partnerships ADD COLUMN price_override INTEGER;
