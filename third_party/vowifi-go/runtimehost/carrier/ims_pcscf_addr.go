package carrier

import "strings"

// ResolveIMSPcscfAddr returns a carrier preset P-CSCF override ("host:port") when set.
func ResolveIMSPcscfAddr(mcc, mnc string, selected ...Preset) string {
	preset, ok := lookup(mcc, mnc)
	if len(selected) > 0 {
		preset = selected[0]
		ok = true
	}
	if !ok {
		return ""
	}
	return strings.TrimSpace(preset.IMSPcscfAddr)
}
