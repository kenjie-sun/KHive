package ikev2

import (
	"fmt"
	"strings"
)

// ConfiguredProposals preserves carrier suite order and fails on unsupported
// settings instead of silently replacing them with unrelated defaults.
// This SWu implementation supports MODP 2048 key exchange only.
func ConfiguredProposals(suites []string, protocol ProtocolID, spi []byte) ([]*Proposal, error) {
	if protocol != ProtoIKE && protocol != ProtoESP {
		return nil, fmt.Errorf("unsupported proposal protocol %d", protocol)
	}
	if len(suites) == 0 {
		if protocol == ProtoESP {
			return CreateMultiProposalESP(spi), nil
		}
		var supported []*Proposal
		for _, p := range CreateMultiProposalIKE(spi) {
			for _, t := range p.Transforms {
				if t.Type == TransformTypeDH && t.ID == MODP_2048_bit {
					p.ProposalNum = uint8(len(supported) + 1)
					supported = append(supported, p)
				}
			}
		}
		return supported, nil
	}
	if len(suites) > 255 {
		return nil, fmt.Errorf("too many proposals")
	}
	var proposals []*Proposal
	for i, suite := range suites {
		parts := strings.Split(strings.ToLower(strings.TrimSpace(suite)), "-")
		if len(parts) < 2 {
			return nil, fmt.Errorf("invalid proposal %q", suite)
		}
		bits := 0
		switch parts[0] {
		case "aes128":
			bits = 128
		case "aes192":
			bits = 192
		case "aes256":
			bits = 256
		default:
			return nil, fmt.Errorf("unsupported encryption in proposal %q", suite)
		}
		integ, prf, ok := proposalHash(parts[1])
		if !ok {
			return nil, fmt.Errorf("unsupported integrity in proposal %q", suite)
		}
		p := NewProposal(uint8(i+1), protocol, spi)
		p.AddTransformWithKeyLen(TransformTypeEncr, ENCR_AES_CBC, bits)
		p.AddTransform(TransformTypeInteg, integ, 0)
		if protocol == ProtoIKE {
			if len(parts) != 3 && len(parts) != 4 {
				return nil, fmt.Errorf("invalid IKE proposal %q", suite)
			}
			if parts[len(parts)-1] != "modp2048" {
				return nil, fmt.Errorf("unsupported DH group in proposal %q", suite)
			}
			if len(parts) == 4 {
				if !strings.HasPrefix(parts[2], "prf") {
					return nil, fmt.Errorf("invalid PRF in proposal %q", suite)
				}
				_, prf, ok = proposalHash(strings.TrimPrefix(parts[2], "prf"))
				if !ok {
					return nil, fmt.Errorf("unsupported PRF in proposal %q", suite)
				}
			}
			p.AddTransform(TransformTypePRF, prf, 0)
			p.AddTransform(TransformTypeDH, MODP_2048_bit, 0)
		} else {
			if len(parts) != 2 {
				return nil, fmt.Errorf("invalid ESP proposal %q", suite)
			}
			p.AddTransform(TransformTypeESN, 0, 0)
		}
		proposals = append(proposals, p)
	}
	return proposals, nil
}

func proposalHash(name string) (AlgorithmType, AlgorithmType, bool) {
	switch name {
	case "sha1":
		return AUTH_HMAC_SHA1_96, PRF_HMAC_SHA1, true
	case "sha256":
		return AUTH_HMAC_SHA2_256_128, PRF_HMAC_SHA2_256, true
	case "sha384":
		return AUTH_HMAC_SHA2_384_192, PRF_HMAC_SHA2_384, true
	case "sha512":
		return AUTH_HMAC_SHA2_512_256, PRF_HMAC_SHA2_512, true
	default:
		return 0, 0, false
	}
}
