package swu

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/1239t/swu-go/pkg/ikev2"
)

// Keep the selected suite unchanged during rekey. The existing crypto objects
// then remain valid for both the old SA and the newly derived key material.
func copySAProposal(src *ikev2.Proposal, number uint8, spi []byte) *ikev2.Proposal {
	if src == nil {
		return nil
	}
	dst := ikev2.NewProposal(number, src.ProtocolID, append([]byte(nil), spi...))
	for _, t := range src.Transforms {
		cp := &ikev2.Transform{Type: t.Type, ID: t.ID}
		for _, a := range t.Attributes {
			cp.Attributes = append(cp.Attributes, &ikev2.TransformAttribute{
				Type: a.Type, Val: a.Val, Value: append([]byte(nil), a.Value...),
			})
		}
		dst.Transforms = append(dst.Transforms, cp)
	}
	return dst
}

func rekeyProposal(selected *ikev2.Proposal, protocol ikev2.ProtocolID, spi []byte) (*ikev2.Proposal, error) {
	if selected == nil || selected.ProtocolID != protocol || len(selected.Transforms) == 0 {
		return nil, errors.New("rekey requires the negotiated SA proposal")
	}
	return copySAProposal(selected, 1, spi), nil
}

// Responses must select precisely what we offered, including attributes. Do not
// silently accept a different key size/PRF while retaining the old crypto objects.
func validateRekeyProposal(sa *ikev2.EncryptedPayloadSA, offered *ikev2.Proposal, spiSize int) error {
	if sa == nil || len(sa.Proposals) != 1 || offered == nil {
		return errors.New("rekey response must contain one SA proposal")
	}
	p := sa.Proposals[0]
	if p == nil || p.ProtocolID != offered.ProtocolID || p.ProposalNum != offered.ProposalNum || len(p.SPI) != spiSize || bytes.Equal(p.SPI, make([]byte, spiSize)) {
		return errors.New("rekey response has an invalid protocol, proposal number or SPI")
	}
	if len(p.Transforms) != len(offered.Transforms) {
		return errors.New("rekey response changed the negotiated transform set")
	}
	seen := make(map[ikev2.TransformType]bool)
	for _, selected := range p.Transforms {
		if selected == nil || seen[selected.Type] {
			return errors.New("duplicate or missing rekey transform")
		}
		seen[selected.Type] = true
		matched := false
		for _, expected := range offered.Transforms {
			if sameTransform(selected, expected) {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("rekey response changed transform type %d", selected.Type)
		}
	}
	return nil
}

func sameTransform(a, b *ikev2.Transform) bool {
	if a == nil || b == nil || a.Type != b.Type || a.ID != b.ID || len(a.Attributes) != len(b.Attributes) {
		return false
	}
	seen := make(map[uint16]bool)
	for _, attr := range a.Attributes {
		if attr == nil || seen[attr.Type] {
			return false
		}
		seen[attr.Type] = true
		matched := false
		for _, expected := range b.Attributes {
			if expected != nil && attr.Type == expected.Type && attr.Val == expected.Val && bytes.Equal(attr.Value, expected.Value) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}
