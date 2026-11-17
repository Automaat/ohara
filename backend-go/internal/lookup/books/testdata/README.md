# Book provider fixtures

- `bn/` and `openlibrary/`: live responses recorded on 2026-11-17, one file
  per query value (`bn/830802534X.json` is the ISBN-10 retry for
  9788308025345).
- `google/`: hand-written in the Google Books `volumes` format. The API gives
  keyless clients no quota, so no live response could be recorded.
