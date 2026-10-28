package core_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"pass-dups/internal/core"
)

func syntheticItem(id string) core.Item {
	return core.Item{ID: id, ShareID: "synthetic", State: "Active", Title: "Synthetic " + id, Extras: map[string]json.RawMessage{}}
}

func TestDroppedDataExcludesValuesPreservedInAnyKeptEntry(t *testing.T) {
	target, source, other := syntheticItem("target"), syntheticItem("source"), syntheticItem("other")
	source.Username = "synthetic-user"
	source.Email = "synthetic@example.com"
	source.Password = "SYNTHETIC-password"
	source.Note = "synthetic note"
	source.URLs = []string{"https://example.com"}
	source.Extras["login.totp_uri"] = json.RawMessage(`"otpauth://SYNTHETIC"`)
	other.Username = source.Username
	other.Email = source.Email
	other.Password = source.Password
	other.Note = "prefix\n\n" + source.Note + "\n\nsuffix"
	other.URLs = append([]string(nil), source.URLs...)
	other.Extras["login.totp_uri"] = source.Extras["login.totp_uri"]
	if got := core.DroppedData([]core.Item{target, source, other}, []string{source.ID}, target); len(got) != 0 {
		t.Fatal("data preserved in another kept entry was highlighted as dropped")
	}
}

func TestDroppedDataIncludesMissingCredentialsURLsAndNotes(t *testing.T) {
	target, source := syntheticItem("target"), syntheticItem("source")
	source.Username = "synthetic-user"
	source.Email = "synthetic@example.com"
	source.Password = "SYNTHETIC-password"
	source.Note = "synthetic note"
	source.URLs = []string{"https://example.com"}
	dropped := core.DroppedData([]core.Item{target, source}, []string{source.ID}, target)
	if len(dropped) != 1 || dropped[0].ID != source.ID || !dropped[0].Trashed || len(dropped[0].Fields) != 4 || !reflect.DeepEqual(dropped[0].URLs, source.URLs) {
		t.Fatal("missing selected-source data not reported accurately")
	}
}

func TestOverwrittenTargetValuesAreHighlighted(t *testing.T) {
	target, source := syntheticItem("target"), syntheticItem("source")
	target.Password = "SYNTHETIC-old"
	source.Password = "SYNTHETIC-new"
	p := core.Plan{TargetID: target.ID, Originals: []core.Item{target, source}, TrashIDs: []string{source.ID}, Fields: map[string]string{"password": source.Password}}
	dropped := p.DroppedData()
	if len(dropped) != 1 || dropped[0].ID != target.ID || dropped[0].Trashed || dropped[0].Fields["password"] != target.Password {
		t.Fatal("overwritten target password was not identified")
	}
}

func TestDroppedCustomFieldsOnlyIncludesMissingArrayElements(t *testing.T) {
	target, source := syntheticItem("target"), syntheticItem("source")
	target.Extras["data.extra_fields"] = json.RawMessage(`[{"name":"shared","content":{"Hidden":"SYNTHETIC-shared"}}]`)
	source.Extras["data.extra_fields"] = json.RawMessage(`[{"content":{"Hidden":"SYNTHETIC-shared"},"name":"shared"},{"name":"unique","content":{"Hidden":"SYNTHETIC-unique"}}]`)
	dropped := core.DroppedData([]core.Item{target, source}, []string{source.ID}, target)
	if len(dropped) != 1 {
		t.Fatal("missing custom field not identified")
	}
	var fields []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(dropped[0].Extras["data.extra_fields"], &fields); err != nil || len(fields) != 1 || fields[0].Name != "unique" {
		t.Fatal("already-preserved custom field was included in dropped information")
	}
}

func TestDroppedURLsRecognizeRetainedAutofillURLs(t *testing.T) {
	target, source := syntheticItem("target"), syntheticItem("source")
	source.URLs = []string{"https://example.com"}
	target.Extras["login.autofill_urls"] = json.RawMessage(`[{"url":"https://example.com","mode":"Default"}]`)
	if dropped := core.DroppedData([]core.Item{target, source}, []string{source.ID}, target); len(dropped) != 0 {
		t.Fatal("URL retained by autofill settings was reported as dropped")
	}
}

func TestAlreadyTrashedDataIsNotReportedAsNewlyDropped(t *testing.T) {
	target, source := syntheticItem("target"), syntheticItem("source")
	source.State = "Trashed"
	source.Password = "SYNTHETIC-password"
	source.Extras["login.totp_uri"] = json.RawMessage(`"SYNTHETIC-totp"`)
	if dropped := core.DroppedData([]core.Item{target, source}, nil, target); len(dropped) != 0 {
		t.Fatal("already-Trashed entry was included in dropped data")
	}
}

func TestKeepNoneHighlightsAllActiveInformation(t *testing.T) {
	a, b := syntheticItem("a"), syntheticItem("b")
	a.Password = "SYNTHETIC-password"
	b.Note = "synthetic note"
	dropped := core.DroppedData([]core.Item{a, b}, []string{a.ID, b.ID}, core.Item{})
	if len(dropped) != 2 || dropped[0].Fields["password"] != a.Password || dropped[1].Fields["note"] != b.Note {
		t.Fatal("keep-none omitted discarded information")
	}
}

func TestExtraComparisonPreservesLargeNumbers(t *testing.T) {
	target, source := syntheticItem("target"), syntheticItem("source")
	target.Extras["future.id"] = json.RawMessage(`9007199254740992`)
	source.Extras["future.id"] = json.RawMessage(`9007199254740993`)
	dropped := core.DroppedData([]core.Item{target, source}, []string{source.ID}, target)
	if len(dropped) != 1 || string(dropped[0].Extras["future.id"]) != "9007199254740993" {
		t.Fatal("extra-field comparison rounded distinct values into equality")
	}
}

func TestNotesMatchWholeBlocksOnly(t *testing.T) {
	target, source := syntheticItem("target"), syntheticItem("source")
	source.Note = "PIN 1234"
	target.Note = "Old PIN 12345"
	dropped := core.DroppedData([]core.Item{target, source}, []string{source.ID}, target)
	if len(dropped) != 1 || dropped[0].Fields["note"] != source.Note {
		t.Fatal("note contained inside a longer kept note was treated as kept")
	}
	target.Note = "first\n\nPIN 1234\n\nlast"
	if dropped := core.DroppedData([]core.Item{target, source}, []string{source.ID}, target); len(dropped) != 0 {
		t.Fatal("note kept as a whole block was reported as dropped")
	}
}

func TestExtraComparisonUsesSubsetsAndNumericValues(t *testing.T) {
	target, source := syntheticItem("target"), syntheticItem("source")
	target.Extras["future.object"] = json.RawMessage(`{"a":1,"b":2}`)
	source.Extras["future.object"] = json.RawMessage(`{"a":1.0}`)
	target.Extras["future.number"] = json.RawMessage(`1`)
	source.Extras["future.number"] = json.RawMessage(`1.0`)
	target.Extras["future.list"] = json.RawMessage(`[{"id":"x","extra":true}]`)
	source.Extras["future.list"] = json.RawMessage(`[{"id":"x"}]`)
	if dropped := core.DroppedData([]core.Item{target, source}, []string{source.ID}, target); len(dropped) != 0 {
		t.Fatal("retained extra data reported as dropped")
	}
	source.Extras["future.object"] = json.RawMessage(`{"a":1,"c":3}`)
	dropped := core.DroppedData([]core.Item{target, source}, []string{source.ID}, target)
	if len(dropped) != 1 || len(dropped[0].Extras) != 1 || dropped[0].Extras["future.object"] == nil {
		t.Fatal("object key missing from kept entry was not reported")
	}
}

func TestPlanResultAppliesFieldsThroughSet(t *testing.T) {
	target, source := syntheticItem("target"), syntheticItem("source")
	target.URLs = []string{"https://old.example.com"}
	p := core.Plan{TargetID: target.ID, Originals: []core.Item{target, source}, Fields: map[string]string{"password": "SYNTHETIC-new", "note": "merged", "urls": "https://a.example.com, https://b.example.com"}}
	result := p.Result()
	if result.Password != "SYNTHETIC-new" || result.Note != "merged" || !reflect.DeepEqual(result.URLs, []string{"https://a.example.com", "https://b.example.com"}) {
		t.Fatal("plan result does not reflect field updates")
	}
	encoded, err := core.EncodeURLs(result.URLs)
	if err != nil || encoded != p.Fields["urls"] {
		t.Fatal("DecodeURLs does not reverse EncodeURLs")
	}
	p.KeepNone = true
	if p.Result().ID != "" {
		t.Fatal("keep-none plan produced a merged result")
	}
}

func TestDroppedListsOmitEmptyElements(t *testing.T) {
	target, source := syntheticItem("target"), syntheticItem("source")
	source.Extras["future.list"] = json.RawMessage(`[{"id":"x"},{},""]`)
	dropped := core.DroppedData([]core.Item{target, source}, []string{source.ID}, target)
	if len(dropped) != 1 || string(dropped[0].Extras["future.list"]) != `[{"id":"x"}]` {
		t.Fatal("empty list elements were reported as dropped")
	}
}
