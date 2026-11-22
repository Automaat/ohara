package deals

const maxCoverageRows = 1000

type queryOutcome int

const (
	querySucceeded queryOutcome = iota
	queryFailed
	queryRefused
	querySkipped
)

type rejectionReason string

const (
	rejectionKind       rejectionReason = "wrong_kind"
	rejectionFormat     rejectionReason = "wrong_format"
	rejectionEdition    rejectionReason = "edition_details"
	rejectionCreator    rejectionReason = "creator_mismatch"
	rejectionTitle      rejectionReason = "title_mismatch"
	rejectionLot        rejectionReason = "lot"
	rejectionKeyword    rejectionReason = "reject_phrase"
	rejectionOtherTitle rejectionReason = "more_specific_title"
	rejectionOtherWish  rejectionReason = "assigned_to_another_wish"
)

type runCoverage struct {
	Rows      []*wishCoverage `json:"rows"`
	Truncated bool            `json:"truncated,omitempty"`
}

type coverageKey struct {
	Source string `json:"source"`
	CopyID int64  `json:"copy_id"`
}

type wishCoverage struct {
	coverageKey
	Replies    int                     `json:"replies"`
	Empty      int                     `json:"empty"`
	Failed     int                     `json:"failed"`
	Refused    int                     `json:"refused"`
	Skipped    int                     `json:"skipped"`
	Candidates int                     `json:"candidates"`
	Matched    int                     `json:"unique_matches"`
	Rejections map[rejectionReason]int `json:"rejections,omitempty"`
}

type coverageTarget struct {
	want *Want
	row  *wishCoverage
}

type coverageMatch struct {
	row     *wishCoverage
	listing string
}

func (r *run) coverageRow(source string, w *Want) *wishCoverage {
	key := coverageKey{Source: source, CopyID: w.CopyID}
	if row := r.coverageRows[key]; row != nil {
		return row
	}
	if len(r.coverageRows) >= maxCoverageRows {
		r.res.coverage.Truncated = true
		return nil
	}
	row := &wishCoverage{coverageKey: key}
	r.coverageRows[key] = row
	r.res.coverage.Rows = append(r.res.coverage.Rows, row)
	return row
}

func (r *run) queryCoverage(source, query string, feed bool, outcome queryOutcome, candidates int) []coverageTarget {
	var targets []coverageTarget
	if feed {
		for i := range r.wants {
			targets = r.addCoverageTarget(targets, source, &r.wants[i], outcome, candidates)
		}
		return targets
	}
	for _, w := range r.queryWants[spelledKey(query)] {
		targets = r.addCoverageTarget(targets, source, w, outcome, candidates)
	}
	return targets
}

func (r *run) addCoverageTarget(targets []coverageTarget, source string, w *Want, outcome queryOutcome, candidates int) []coverageTarget {
	if !allowsKind(r.kinds[source], w.Kind) {
		return targets
	}
	row := r.coverageRow(source, w)
	if row == nil {
		return targets
	}
	switch outcome {
	case queryFailed:
		row.Failed++
	case queryRefused:
		row.Refused++
	case querySkipped:
		row.Skipped++
	case querySucceeded:
		row.Replies++
		if candidates == 0 {
			row.Empty++
		}
	}
	if candidates == 0 {
		return targets
	}
	return append(targets, coverageTarget{want: w, row: row})
}

func (r *run) coverListing(source string, targets []coverageTarget, l *Listing, text *ListingText, matched *Want) {
	var matchedRow *wishCoverage
	for _, target := range targets {
		target.row.Candidates++
		if target.want == matched {
			matchedRow = target.row
			continue
		}
		reason := rejectionKind
		if allowsKind(l.Kinds, target.want.Kind) {
			reason = text.rejection(target.want, r.titles)
			if reason == "" {
				reason = rejectionOtherWish
			}
		}
		if target.row.Rejections == nil {
			target.row.Rejections = make(map[rejectionReason]int)
		}
		target.row.Rejections[reason]++
	}
	if matched == nil {
		return
	}
	if matchedRow == nil {
		matchedRow = r.coverageRow(source, matched)
		if matchedRow == nil {
			return
		}
		matchedRow.Candidates++
	}
	key := coverageMatch{row: matchedRow, listing: listingKey(l.Source, l.ExternalID)}
	if _, seen := r.coverageMatches[key]; !seen {
		r.coverageMatches[key] = struct{}{}
		matchedRow.Matched++
	}
}
