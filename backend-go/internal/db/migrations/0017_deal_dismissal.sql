-- When the collector dismissed a deal listing on Okazje ("Ignoruj"). A
-- dismissed listing leaves the deals list and the badge and its unsent
-- alert is never sent, for every wish it matches; the valuation still uses
-- its price. Null while the listing is shown.
ALTER TABLE price_observation ADD COLUMN dismissed_at timestamptz;
