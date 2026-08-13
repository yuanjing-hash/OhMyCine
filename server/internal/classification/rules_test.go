package classification

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestDefaultRulesAndMatcherContract(t *testing.T) {
	rules := DefaultRules()
	if err := Validate(rules); err != nil {
		t.Fatal(err)
	}
	wantMovie := []string{"动画电影", "华语电影", "外语电影"}
	wantTV := []string{"国漫", "日番", "动漫", "纪录片", "儿童", "综艺", "国产剧", "欧美剧", "日韩剧"}
	if got := categoryNames(rules.Groups[0]); !reflect.DeepEqual(got, wantMovie) {
		t.Fatalf("movie=%v", got)
	}
	if got := categoryNames(rules.Groups[1]); !reflect.DeepEqual(got, wantTV) {
		t.Fatalf("tv=%v", got)
	}
	cases := []struct {
		name     string
		metadata Metadata
		want     string
	}{
		{"movie first match", Metadata{MediaType: MediaTypeMovie, GenreIDs: []int{16}, OriginalLanguage: "zh"}, "动画电影"},
		{"movie language case", Metadata{MediaType: MediaTypeMovie, OriginalLanguage: "ZH"}, "华语电影"},
		{"movie foreign", Metadata{MediaType: MediaTypeMovie, OriginalLanguage: "en"}, "外语电影"},
		{"movie exclude-only accepts missing metadata", Metadata{MediaType: MediaTypeMovie}, "外语电影"},
		{"tv include or dimensions and", Metadata{MediaType: MediaTypeTV, GenreIDs: []int{16}, OriginCountries: []string{"tw"}}, "国漫"},
		{"tv animation order", Metadata{MediaType: MediaTypeTV, GenreIDs: []int{16}, OriginCountries: []string{"US"}}, "动漫"},
		{"tv documentary", Metadata{MediaType: MediaTypeTV, GenreIDs: []int{99}}, "纪录片"},
		{"tv fallback", Metadata{MediaType: MediaTypeTV}, "未分类"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := Classify(test.metadata, rules).CategoryName; got != test.want {
				t.Fatalf("got %q want %q", got, test.want)
			}
		})
	}
}

func TestMatcherExcludeAndInclusiveYear(t *testing.T) {
	from, to := 2000, 2010
	rules := EmptyRules()
	rules.Groups[0].Categories = []CategoryRuleV1{{ID: "x", Name: "X", Conditions: ConditionsV1{GenreIDs: SetCondition[int]{Include: []int{16}, Exclude: []int{99}}, OriginalLanguages: SetCondition[string]{}, ReleaseYear: &YearRange{From: &from, To: &to}}}}
	for _, year := range []int{2000, 2010} {
		if got := Classify(Metadata{MediaType: MediaTypeMovie, GenreIDs: []int{16}, ReleaseYear: &year}, rules).CategoryName; got != "X" {
			t.Fatal(got)
		}
	}
	year := 2005
	if got := Classify(Metadata{MediaType: MediaTypeMovie, GenreIDs: []int{16, 99}, ReleaseYear: &year}, rules).CategoryName; got != "未分类" {
		t.Fatal(got)
	}
}

func TestStrictValidationRejectsUnknownDuplicateAndWrongDimension(t *testing.T) {
	rules := EmptyRules()
	payload, _ := json.Marshal(rules)
	payload = append(payload[:len(payload)-1], []byte(`,"unknown":true}`)...)
	if _, err := DecodeStrict(payload); err == nil {
		t.Fatal("unknown field accepted")
	}
	rules.Groups[0].Categories = []CategoryRuleV1{{ID: "a", Name: "A", Conditions: ConditionsV1{GenreIDs: SetCondition[int]{Include: []int{16, 16}}, OriginalLanguages: SetCondition[string]{}}}}
	if err := Validate(rules); err == nil {
		t.Fatal("duplicate accepted")
	}
	rules.Groups[0].Categories[0].Conditions.GenreIDs.Include = []int{}
	rules.Groups[0].Categories[0].Conditions.OriginCountries = &SetCondition[string]{}
	if err := Validate(rules); err == nil {
		t.Fatal("tv field accepted for movie")
	}
}

func TestStrictValidationRequiresConditionFields(t *testing.T) {
	payload := []byte(`{"version":1,"groups":[{"media_type":"movie","categories":[{"id":"x","name":"X","conditions":{"original_languages":{"include":[],"exclude":[]},"release_year":null}}],"fallback_category_name":"未分类"},{"media_type":"tv","categories":[],"fallback_category_name":"未分类"}]}`)
	if _, err := DecodeStrict(payload); err == nil {
		t.Fatal("missing genre_ids accepted")
	}
}

func TestStrictValidationRejectsUnknownNestedFieldsAndInvalidScalars(t *testing.T) {
	base, _ := json.Marshal(DefaultRules())
	cases := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"unknown group field", func(root map[string]any) { root["groups"].([]any)[0].(map[string]any)["mystery"] = true }},
		{"unknown condition field", func(root map[string]any) {
			root["groups"].([]any)[0].(map[string]any)["categories"].([]any)[0].(map[string]any)["conditions"].(map[string]any)["mystery"] = true
		}},
		{"fractional year", func(root map[string]any) {
			root["groups"].([]any)[0].(map[string]any)["categories"].([]any)[0].(map[string]any)["conditions"].(map[string]any)["release_year"] = map[string]any{"from": 2000.5}
		}},
		{"wrong media dimension", func(root map[string]any) {
			root["groups"].([]any)[0].(map[string]any)["categories"].([]any)[0].(map[string]any)["conditions"].(map[string]any)["origin_countries"] = map[string]any{"include": []any{}, "exclude": []any{}}
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			var root map[string]any
			if err := json.Unmarshal(base, &root); err != nil {
				t.Fatal(err)
			}
			test.mutate(root)
			payload, _ := json.Marshal(root)
			if _, err := DecodeStrict(payload); err == nil {
				t.Fatal("invalid payload accepted")
			}
		})
	}
}

func TestCategoryIDsAreUniqueAcrossProfile(t *testing.T) {
	rules := EmptyRules()
	rules.Groups[0].Categories = []CategoryRuleV1{{ID: "shared", Name: "Movie", Conditions: ConditionsV1{GenreIDs: SetCondition[int]{}, OriginalLanguages: SetCondition[string]{}}}}
	rules.Groups[1].Categories = []CategoryRuleV1{{ID: "shared", Name: "TV", Conditions: ConditionsV1{GenreIDs: SetCondition[int]{}, OriginalLanguages: SetCondition[string]{}}}}
	if err := Validate(rules); err == nil {
		t.Fatal("cross-group duplicate category id accepted")
	}
}

func TestCloneRegeneratesCategoryIDs(t *testing.T) {
	source := DefaultRules()
	clone, err := Clone(source, true)
	if err != nil {
		t.Fatal(err)
	}
	if clone.Groups[0].Categories[0].ID == source.Groups[0].Categories[0].ID {
		t.Fatal("id was not regenerated")
	}
	clone.Groups[0].Categories[0].Name = "changed"
	if source.Groups[0].Categories[0].Name == "changed" {
		t.Fatal("clone mutated source")
	}
}

func categoryNames(group RuleGroupV1) []string {
	result := make([]string, len(group.Categories))
	for i, category := range group.Categories {
		result[i] = category.Name
	}
	return result
}
