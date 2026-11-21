# Discogs answers

Recorded from the live API on 2026-11-21 without a token:

| File | Request | Status |
| :-- | :-- | :-- |
| `stats_for_sale.json` | `GET /marketplace/stats/1873013?curr_abbr=EUR` | 200 |
| `stats_none_for_sale.json` | `GET /marketplace/stats/30000001?curr_abbr=EUR` | 200 |
| `stats_not_found.json` | `GET /marketplace/stats/30000017?curr_abbr=EUR` | 404 |
| `price_suggestions_unauthenticated.json` | `GET /marketplace/price_suggestions/1873013` | 401 |

`price_suggestions.json` follows the example in the Discogs API
documentation: recording it needs a token whose owner has filled in the
seller settings.
