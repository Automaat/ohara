package apitypes

import (
	"strings"
	"testing"
	"time"
)

type day struct{ time.Time }

func (d day) MarshalJSON() ([]byte, error) { return []byte(`"2026-01-02"`), nil }

type base struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type inner struct {
	Cents int64 `json:"cents"`
}

type sample struct {
	base
	Name     string     `json:"name" ts:"Color"`
	Note     *string    `json:"note"`
	Cover    *string    `json:"cover,omitempty"`
	Failure  string     `json:"failure,omitempty"`
	Version  string     `json:"version,omitzero"`
	Year     *int       `json:"year,omitempty" ts:",null"`
	Shade    *string    `json:"shade,omitzero" ts:"Color,null"`
	Tags     []string   `json:"tags" ts:",null"`
	Colors   []string   `json:"colors" ts:"Color"`
	Maybe    *string    `json:"maybe" ts:"Color"`
	Seen     time.Time  `json:"seen"`
	Last     *time.Time `json:"last"`
	On       day        `json:"on"`
	Inner    inner      `json:"inner"`
	Next     *inner     `json:"next"`
	Rate     float64    `json:"rate"`
	OK       bool       `json:"ok"`
	Untagged int
	Skipped  string `json:"-"`
}

func generate(t *testing.T, api API) (string, error) {
	t.Helper()
	var b strings.Builder
	err := Generate(&b, "// header\n", api)
	return b.String(), err
}

func sampleAPI() API {
	return API{
		Enums:   []Enum{{Name: "Color", Values: []string{"red", "it's"}}},
		Scalars: []Scalar{ScalarOf[time.Time](), ScalarOf[day]()},
		Types:   []Named{Of[inner]("Inner"), Of[sample]("Sample")},
	}
}

func TestGenerateWritesFieldsAsJSONEncodesThem(t *testing.T) {
	got, err := generate(t, sampleAPI())
	if err != nil {
		t.Fatal(err)
	}
	want := `// header

export type Color = 'red' | 'it\'s';

export type Inner = {
	cents: number;
};

export type Sample = {
	id: number;
	name: Color;
	note: string | null;
	cover?: string;
	failure?: string;
	version?: string;
	year?: number | null;
	shade?: Color | null;
	tags: string[] | null;
	colors: Color[];
	maybe: Color | null;
	seen: string;
	last: string | null;
	on: string;
	inner: Inner;
	next: Inner | null;
	rate: number;
	ok: boolean;
	Untagged: number;
};
`
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestGenerateWrapsLongUnions(t *testing.T) {
	values := []string{"aaaaaaaaaaaa", "bbbbbbbbbbbb", "cccccccccccc", "dddddddddddd", "eeeeeeeeeeee", "ffffffffffff"}
	got, err := generate(t, API{Enums: []Enum{{Name: "Long", Values: values}}})
	if err != nil {
		t.Fatal(err)
	}
	want := "// header\n\nexport type Long =\n\t| 'aaaaaaaaaaaa'\n\t| 'bbbbbbbbbbbb'\n\t| 'cccccccccccc'\n" +
		"\t| 'dddddddddddd'\n\t| 'eeeeeeeeeeee'\n\t| 'ffffffffffff';\n"
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

type unknownEnum struct {
	Kind string `json:"kind" ts:"Nope"`
}

type enumOnNumber struct {
	Count int `json:"count" ts:"Color"`
}

type nested struct {
	Inner inner `json:"inner"`
}

type selfMarshaled struct {
	On day `json:"on"`
}

type stringOption struct {
	Count int `json:"count,string"`
}

type unknownTSOption struct {
	Name string `json:"name" ts:",maybe"`
}

type withMap struct {
	Tags map[string]string `json:"tags"`
}

type withAny struct {
	Data any `json:"data"`
}

func TestGenerateRefusesWhatItCannotName(t *testing.T) {
	color := []Enum{{Name: "Color", Values: []string{"red"}}}
	tests := []struct {
		name string
		api  API
		want string
	}{
		{"unknown enum", API{Types: []Named{Of[unknownEnum]("T")}}, `unknown enum "Nope"`},
		{"enum on a number", API{Enums: color, Types: []Named{Of[enumOnNumber]("T")}}, "only strings"},
		{"struct without a name", API{Types: []Named{Of[nested]("T")}}, "has no TypeScript name"},
		{"marshaler not listed as scalar", API{Types: []Named{Of[selfMarshaled]("T")}}, "marshals itself"},
		{"json string option", API{Types: []Named{Of[stringOption]("T")}}, `json tag option "string"`},
		{"unknown ts option", API{Types: []Named{Of[unknownTSOption]("T")}}, `ts tag option "maybe"`},
		{"map", API{Types: []Named{Of[withMap]("T")}}, "not supported"},
		{"interface", API{Types: []Named{Of[withAny]("T")}}, "not supported"},
		{"name used twice", API{Enums: color, Types: []Named{Of[inner]("Color")}}, "used twice"},
		{"type named twice", API{Types: []Named{Of[inner]("A"), Of[inner]("B")}}, "named twice"},
		{"enum without values", API{Enums: []Enum{{Name: "Empty"}}}, "no values"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := generate(t, tt.api)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want one containing %q", err, tt.want)
			}
		})
	}
}

func TestOharaKeepsTheSPAsUnionsAndNullability(t *testing.T) {
	got, err := generate(t, Ohara())
	if err != nil {
		t.Fatal(err)
	}
	item := block(t, got, "CollectionItem")
	for _, want := range []string{
		"\tstatus: CopyStatus;\n",
		"\tkind: Kind;\n",
		"\tprice_cents: number | null;\n",
		"\tcover_url?: string;\n",
		"\tpurchased_on: string | null;\n",
	} {
		if !strings.Contains(item, want) {
			t.Errorf("CollectionItem lacks %q:\n%s", want, item)
		}
	}
	edition := block(t, got, "CandidateEdition")
	if !strings.Contains(edition, "\tformat?: Format | null;\n") {
		t.Errorf("CandidateEdition lost its nullable format:\n%s", edition)
	}
	for _, want := range []string{
		"export type CopyStatus = 'owned' | 'wishlist' | 'sold' | 'ordered';\n",
		"export type Kind = 'movie' | 'book' | 'music';\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output lacks %q", want)
		}
	}
}

func block(t *testing.T, src, name string) string {
	t.Helper()
	start := strings.Index(src, "export type "+name+" = {")
	if start < 0 {
		t.Fatalf("no type %s", name)
	}
	end := strings.Index(src[start:], "\n};\n")
	return src[start : start+end]
}
