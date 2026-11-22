-- TV series: a title of kind tv is one season of one show. Its tmdb_id
-- names the TMDB show, so the seasons of a show share it; a film's
-- tmdb_id stays unique among films, and a show and a film may carry the
-- same TMDB number, since TMDB numbers films and shows apart.

ALTER TABLE title DROP CONSTRAINT title_kind_check;
ALTER TABLE title ADD CONSTRAINT title_kind_check
    CHECK (kind IN ('movie', 'book', 'music', 'tv'));

ALTER TABLE title ADD COLUMN season integer CHECK (season >= 0);
ALTER TABLE title ADD CONSTRAINT title_season_kind_check
    CHECK ((kind = 'tv') = (season IS NOT NULL));

DROP INDEX title_tmdb_id_key;
CREATE UNIQUE INDEX title_tmdb_id_key ON title (tmdb_id)
    WHERE tmdb_id IS NOT NULL AND kind <> 'tv';
CREATE UNIQUE INDEX title_tv_season_key ON title (tmdb_id, season)
    WHERE tmdb_id IS NOT NULL AND kind = 'tv';
