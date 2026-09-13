package swu

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/binary"
	"testing"

	"github.com/1239t/swu-go/pkg/crypto"
	"github.com/1239t/swu-go/pkg/ikev2"
)

func fixtureProposal(t *testing.T, suite string, proto ikev2.ProtocolID, spi []byte) *ikev2.Proposal {
	t.Helper()
	ps, err := ikev2.ConfiguredProposals([]string{suite}, proto, spi)
	if err != nil {
		t.Fatal(err)
	}
	return ps[0]
}

func TestKHiveRekeyPreservesNegotiatedSuiteOnWire(t *testing.T) {
	for _, suite := range []string{"aes256-sha256-prfsha1-modp2048", "aes128-sha256-modp2048", "aes192-sha512-prfsha512-modp2048"} {
		t.Run(suite, func(t *testing.T) {
			selected := fixtureProposal(t, suite, ikev2.ProtoIKE, nil)
			selected.ProposalNum = 4
			offered, err := rekeyProposal(selected, ikev2.ProtoIKE, []byte{1, 2, 3, 4, 5, 6, 7, 8})
			if err != nil {
				t.Fatal(err)
			}
			body, err := (&ikev2.EncryptedPayloadSA{Proposals: []*ikev2.Proposal{offered}}).Encode()
			if err != nil {
				t.Fatal(err)
			}
			wire, err := ikev2.DecodePayloadSA(body)
			if err != nil {
				t.Fatal(err)
			}
			expected := copySAProposal(selected, 1, nil)
			if err := validateRekeyProposal(wire, expected, 8); err != nil {
				t.Fatal(err)
			}
			// The original selected proposal must not be mutated by encoding or rekey.
			offered.Transforms[0].Attributes[0].Val = 128
			if suite[:6] == "aes256" && selected.Transforms[0].Attributes[0].Val != 256 {
				t.Fatal("aliased negotiated attributes")
			}
			if selected.ProposalNum != 4 || len(selected.SPI) != 0 {
				t.Fatal("mutated original proposal")
			}
		})
	}
	esp := fixtureProposal(t, "aes256-sha256", ikev2.ProtoESP, nil)
	child, err := rekeyProposal(esp, ikev2.ProtoESP, []byte{1, 2, 3, 4})
	if err != nil {
		t.Fatal(err)
	}
	if child.Transforms[0].Attributes[0].Val != 256 {
		t.Fatal("Child rekey lost AES256")
	}
	if _, err := rekeyProposal(nil, ikev2.ProtoIKE, nil); err == nil {
		t.Fatal("missing negotiation silently used defaults")
	}
}

func TestKHiveRekeyRejectsChangedResponse(t *testing.T) {
	offered := fixtureProposal(t, "aes256-sha256-prfsha1-modp2048", ikev2.ProtoIKE, nil)
	for _, tc := range []struct {
		name   string
		mutate func(*ikev2.Proposal)
	}{
		{"key-length", func(p *ikev2.Proposal) { p.Transforms[0].Attributes[0].Val = 128 }},
		{"prf", func(p *ikev2.Proposal) { p.Transforms[2].ID = ikev2.PRF_HMAC_SHA2_256 }},
		{"dh", func(p *ikev2.Proposal) { p.Transforms[3].ID = ikev2.MODP_1024_bit }},
		{"protocol", func(p *ikev2.Proposal) { p.ProtocolID = ikev2.ProtoESP }},
		{"spi", func(p *ikev2.Proposal) { p.SPI = p.SPI[:4] }},
		{"zero-spi", func(p *ikev2.Proposal) { p.SPI = make([]byte, 8) }},
		{"proposal-number", func(p *ikev2.Proposal) { p.ProposalNum = 2 }},
		{"duplicate-transform", func(p *ikev2.Proposal) { p.Transforms[3] = p.Transforms[2] }},
		{"missing-attribute", func(p *ikev2.Proposal) { p.Transforms[0].Attributes = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := copySAProposal(offered, 1, []byte{8, 7, 6, 5, 4, 3, 2, 1})
			tc.mutate(p)
			if err := validateRekeyProposal(&ikev2.EncryptedPayloadSA{Proposals: []*ikev2.Proposal{p}}, offered, 8); err == nil {
				t.Fatal("accepted unoffered suite")
			}
		})
	}
}

func newTestDH(t *testing.T) *crypto.DiffieHellman {
	t.Helper()
	d, err := crypto.NewDiffieHellman(14)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.GenerateKey(); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestKHiveO2InitialAndEncryptedRekey(t *testing.T) {
	s := NewSession(&Config{}, nil)
	s.ctx = context.Background()
	s.SPIi, s.SPIr = 1, 2
	s.DH, s.ni = newTestDH(t), bytes.Repeat([]byte{1}, 32)
	initialPeer := newTestDH(t)
	packet := ikev2.NewIKEPacket()
	packet.Header.Version, packet.Header.SPIi, packet.Header.SPIr = 0x20, 1, 2
	packet.Header.ExchangeType, packet.Header.Flags = ikev2.IKE_SA_INIT, ikev2.FlagResponse
	packet.Payloads = []ikev2.Payload{
		&ikev2.EncryptedPayloadSA{Proposals: []*ikev2.Proposal{fixtureProposal(t, "aes256-sha256-prfsha1-modp2048", ikev2.ProtoIKE, nil)}},
		&ikev2.EncryptedPayloadKE{DHGroup: ikev2.MODP_2048_bit, KEData: initialPeer.PublicKeyBytes()},
		&ikev2.EncryptedPayloadNonce{NonceData: bytes.Repeat([]byte{2}, 32)},
	}
	raw, err := packet.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.handleIKESAInitResp(raw); err != nil {
		t.Fatal(err)
	}
	if s.ikeProposal == nil || s.EncAlg.KeySize() != 32 || s.PRFAlg.KeyLen() != 20 {
		t.Fatal("initial negotiation not retained")
	}
	oldKeys := s.Keys
	localDH, remoteDH := newTestDH(t), newTestDH(t)
	ni, nr := bytes.Repeat([]byte{3}, 32), bytes.Repeat([]byte{4}, 32)
	resp := []ikev2.Payload{
		&ikev2.EncryptedPayloadSA{Proposals: []*ikev2.Proposal{copySAProposal(s.ikeProposal, 1, []byte{0, 0, 0, 0, 0, 0, 0, 4})}},
		&ikev2.EncryptedPayloadNonce{NonceData: nr},
		&ikev2.EncryptedPayloadKE{DHGroup: ikev2.MODP_2048_bit, KEData: remoteDH.PublicKeyBytes()},
	}
	// Model the responder's outgoing direction using the old SA's SK_er/SK_ar.
	peer := &Session{SPIi: 1, SPIr: 2, EncAlg: s.EncAlg, IntegAlg: s.IntegAlg, Keys: &ikev2.IKESAKeys{SK_ei: oldKeys.SK_er, SK_ai: oldKeys.SK_ar}}
	raw, err = peer.encryptAndWrapWithMsgID(resp, ikev2.CREATE_CHILD_SA, 9, true)
	if err != nil {
		t.Fatal(err)
	}
	raw[19] = byte(ikev2.FlagResponse)
	icvSize := s.IntegAlg.OutputSize()
	copy(raw[len(raw)-icvSize:], s.IntegAlg.Compute(oldKeys.SK_ar, raw[:len(raw)-icvSize]))
	if err := s.handleRekeyIKESAResp(raw, ni, localDH, 3, oldKeys.SK_d, 1, 2); err != nil {
		t.Fatal(err)
	}
	if s.SPIi != 3 || s.SPIr != 4 || s.SequenceNumber.Load() != 0 {
		t.Fatal("rekey did not switch SA")
	}
	if len(s.Keys.SK_d) != 20 || len(s.Keys.SK_ei) != 32 || len(s.Keys.SK_ai) != 32 {
		t.Fatal("key layout disagrees with O2 suite")
	}
	// Independently check SHA1 SKEYSEED and the first PRF+ block (SK_d).
	mac := hmac.New(sha1.New, oldKeys.SK_d)
	mac.Write(localDH.SharedKey)
	mac.Write(ni)
	mac.Write(nr)
	prf := hmac.New(sha1.New, mac.Sum(nil))
	prf.Write(ni)
	prf.Write(nr)
	spis := make([]byte, 16)
	binary.BigEndian.PutUint64(spis[:8], 3)
	binary.BigEndian.PutUint64(spis[8:], 4)
	prf.Write(spis)
	prf.Write([]byte{1})
	if !bytes.Equal(s.Keys.SK_d, prf.Sum(nil)) {
		t.Fatal("new SK_d does not use negotiated PRF-SHA1")
	}
}
