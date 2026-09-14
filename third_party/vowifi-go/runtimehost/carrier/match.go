package carrier

import (
	"encoding/hex"
	"fmt"
	"strings"
)

// Match uses AND between populated fields; values within a field are OR.
// GIDs and SPN are exact matches, IMSI/ICCID use explicit decimal prefixes.
type Match struct {
	IMSIPrefix  []string `json:"imsi_prefix,omitempty"`
	ICCIDPrefix []string `json:"iccid_prefix,omitempty"`
	SPN         []string `json:"spn,omitempty"`
	GID1        []string `json:"gid1,omitempty"`
	GID2        []string `json:"gid2,omitempty"`
}

func (m *Match) validate() error {
	if m == nil {
		return nil
	}
	if len(m.IMSIPrefix)+len(m.ICCIDPrefix)+len(m.SPN)+len(m.GID1)+len(m.GID2) == 0 {
		return fmt.Errorf("empty match")
	}
	for _, values := range [][]string{m.IMSIPrefix, m.ICCIDPrefix} {
		for _, v := range values {
			if v == "" || strings.Trim(v, "0123456789") != "" {
				return fmt.Errorf("identity prefixes must be decimal digits")
			}
		}
	}
	for _, values := range [][]string{m.GID1, m.GID2} {
		for _, v := range values {
			if b, err := hex.DecodeString(v); err != nil || len(b) == 0 {
				return fmt.Errorf("GID must be nonempty hexadecimal bytes")
			}
		}
	}
	for _, v := range m.SPN {
		if strings.TrimSpace(v) == "" {
			return fmt.Errorf("SPN cannot be empty")
		}
	}
	return nil
}
func matchValues(values []string, actual string, prefix bool) (int, bool) {
	if len(values) == 0 {
		return 0, true
	}
	actual = strings.TrimSpace(actual)
	if actual == "" {
		return 0, false
	}
	best := -1
	for _, v := range values {
		v = strings.TrimSpace(v)
		if (!prefix && strings.EqualFold(actual, v)) || (prefix && strings.HasPrefix(actual, v)) {
			if len(v) > best {
				best = len(v)
			}
		}
	}
	if best < 0 {
		return 0, false
	}
	return best, true
}
func (m *Match) score(in EffectiveCarrierConfigInput) (int, bool) {
	score := 0
	fields := []struct {
		values []string
		actual string
		prefix bool
	}{{m.IMSIPrefix, in.IMSI, true}, {m.ICCIDPrefix, in.ICCID, true}, {m.SPN, in.SPN, false}, {m.GID1, in.GID1, false}, {m.GID2, in.GID2, false}}
	for _, f := range fields {
		n, ok := matchValues(f.values, f.actual, f.prefix)
		if !ok {
			return 0, false
		}
		if len(f.values) > 0 {
			score += 1000
			if f.prefix {
				score += n
			}
		}
	}
	return score, true
}
func resolvePreset(in EffectiveCarrierConfigInput) (Preset, bool, string, error) {
	mu.RLock()
	defer mu.RUnlock()
	var chosen Preset
	found := false
	best := -1
	ambiguous := false
	for _, p := range matchingPresets {
		if plmnKey(p.MCC, p.MNC) != plmnKey(in.MCC, in.MNC) {
			continue
		}
		score, ok := p.Match.score(in)
		if !ok {
			continue
		}
		if !found || p.Priority > chosen.Priority || (p.Priority == chosen.Priority && score > best) {
			chosen = p
			found = true
			best = score
			ambiguous = false
		} else if p.Priority == chosen.Priority && score == best {
			ambiguous = true
		}
	}
	if ambiguous {
		return Preset{}, false, "ambiguous", fmt.Errorf("carrier: equally ranked SIM rules match; set distinct priorities")
	}
	if found {
		return chosen, true, "sim_rule", nil
	}
	key := plmnKey(in.MCC, in.MNC)
	if p, ok := presets[key]; ok {
		return p, true, "plmn_override", nil
	}
	p, ok := builtinDefaults[key]
	if ok {
		return p, true, "builtin", nil
	}
	return Preset{}, false, "3gpp_default", nil
}
