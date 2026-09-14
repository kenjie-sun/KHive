package carrier

import (
	"os"
	"path/filepath"
	"testing"
)

func TestKHiveSIMRulesAndLegacyFallback(t *testing.T) {
	ClearCarrierOverrides()
	t.Cleanup(ClearCarrierOverrides)
	path := filepath.Join(t.TempDir(), "rules.json")
	data := `[
 {"id":"host","mcc":"234","mnc":"10","epdg_addr":"host.invalid"},
 {"id":"brand","mcc":"234","mnc":"10","match":{"spn":["Brand"],"gid1":["01","02"]},"epdg_addr":"brand.invalid","ims_pcscf_addr":"pcscf.invalid:5060"},
 {"id":"specific","mcc":"234","mnc":"10","match":{"imsi_prefix":["23410099"],"spn":["Brand"],"gid1":["01"]},"epdg_addr":"specific.invalid"}
 ]`
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCarrierOverrides(path); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		in           EffectiveCarrierConfigInput
		want, source string
	}{
		{EffectiveCarrierConfigInput{MCC: "234", MNC: "010"}, "host", "plmn_override"},
		{EffectiveCarrierConfigInput{MCC: "234", MNC: "10", SPN: "brand", GID1: "02"}, "brand", "sim_rule"},
		{EffectiveCarrierConfigInput{MCC: "234", MNC: "10", SPN: "Brand", GID1: "01", IMSI: "234100990000001"}, "specific", "sim_rule"},
		{EffectiveCarrierConfigInput{MCC: "234", MNC: "10", SPN: "Brand", GID1: "0100"}, "host", "plmn_override"},
		{EffectiveCarrierConfigInput{MCC: "999", MNC: "01"}, "3gpp-default", "3gpp_default"},
	}
	for _, tc := range cases {
		got := ResolveEffectiveCarrierConfig(tc.in)
		if got.MatchError != nil || got.PresetID != tc.want || got.MatchSource != tc.source {
			t.Fatalf("got %+v want %s", got, tc.want)
		}
	}
	// Invalid reload must leave active rules intact.
	_ = os.WriteFile(path, []byte(`[{"id":"bad","mcc":"234","mnc":"10","match":{"gid1":["ZZ"]}}]`), 0600)
	if _, err := LoadCarrierOverrides(path); err == nil {
		t.Fatal("invalid hex accepted")
	}
	if got := ResolveEffectiveCarrierConfig(cases[1].in); got.PresetID != "brand" {
		t.Fatal("failed load replaced rules")
	}
}
func TestKHiveSIMRulesAmbiguityAndPriority(t *testing.T) {
	ClearCarrierOverrides()
	t.Cleanup(ClearCarrierOverrides)
	path := filepath.Join(t.TempDir(), "rules.json")
	write := func(priority string) {
		t.Helper()
		data := `[{"id":"one","mcc":"234","mnc":"10","match":{"spn":["brand"]}},{"id":"two","mcc":"234","mnc":"10","priority":` + priority + `,"match":{"gid1":["01"]}}]`
		_ = os.WriteFile(path, []byte(data), 0600)
		if _, err := LoadCarrierOverrides(path); err != nil {
			t.Fatal(err)
		}
	}
	in := EffectiveCarrierConfigInput{MCC: "234", MNC: "10", SPN: "brand", GID1: "01"}
	write("0")
	if got := ResolveEffectiveCarrierConfig(in); got.MatchError == nil {
		t.Fatal("ambiguous rules silently selected")
	}
	write("1")
	if got := ResolveEffectiveCarrierConfig(in); got.MatchError != nil || got.PresetID != "two" {
		t.Fatalf("priority %+v", got)
	}
}
