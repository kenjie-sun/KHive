package ikev2

import "testing"

func TestCarrierProposalWireRoundTrip(t *testing.T) {
	ps, err := ConfiguredProposals([]string{"aes256-sha256-prfsha1-modp2048", "aes128-sha512-modp2048"}, ProtoIKE, nil)
	if err != nil {
		t.Fatal(err)
	}
	p := NewIKEPacket()
	p.Header.Version = 0x20
	p.Payloads = []Payload{&EncryptedPayloadSA{Proposals: ps}}
	raw, err := p.Encode()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodePacket(raw)
	if err != nil {
		t.Fatal(err)
	}
	got := decoded.Payloads[0].(*EncryptedPayloadSA).Proposals
	if len(got) != 2 {
		t.Fatal("carrier proposal count changed")
	}
	if got[0].Transforms[2].ID != PRF_HMAC_SHA1 || got[1].Transforms[2].ID != PRF_HMAC_SHA2_512 {
		t.Fatal("carrier PRF ignored")
	}
	if got[0].Transforms[0].Attributes[0].Val != 256 || got[1].Transforms[0].Attributes[0].Val != 128 {
		t.Fatal("key length changed")
	}
	esp, err := ConfiguredProposals([]string{"aes256-sha512"}, ProtoESP, []byte{1, 2, 3, 4})
	if err != nil {
		t.Fatal(err)
	}
	if esp[0].Transforms[1].ID != AUTH_HMAC_SHA2_512_256 || len(esp[0].SPI) != 4 {
		t.Fatal("ESP suite lost")
	}
}
func TestProposalDefaultsOnlyAdvertiseImplementedDH(t *testing.T) {
	ps, err := ConfiguredProposals(nil, ProtoIKE, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range ps {
		for _, tr := range p.Transforms {
			if tr.Type == TransformTypeDH && tr.ID != MODP_2048_bit {
				t.Fatal("unsupported DH advertised")
			}
		}
	}
	for _, bad := range []string{"aes256-sha256-modp1024", "aes256-sha256-prfunknown-modp2048", "aes128-invalid-modp2048"} {
		if _, err := ConfiguredProposals([]string{bad}, ProtoIKE, nil); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
}
