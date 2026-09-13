package swu

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"testing"
	"time"

	"github.com/1239t/swu-go/pkg/crypto"
	"github.com/1239t/swu-go/pkg/ikev2"
	"github.com/1239t/swu-go/pkg/ipsec"
)

type rekeyTestTransport struct {
	sent [][]byte
	fail bool
}

func (*rekeyTestTransport) Start() {}
func (*rekeyTestTransport) Stop()  {}
func (t *rekeyTestTransport) SendIKE(b []byte) error {
	if t.fail {
		return errors.New("simulated send failure")
	}
	t.sent = append(t.sent, append([]byte(nil), b...))
	return nil
}
func (*rekeyTestTransport) SendESP([]byte) error                 { return errors.New("unexpected ESP send") }
func (*rekeyTestTransport) IKEPackets() <-chan []byte            { return nil }
func (*rekeyTestTransport) ESPPackets() <-chan []byte            { return nil }
func (*rekeyTestTransport) NetEventsChan() <-chan ipsec.NetEvent { return nil }

func rekeyPair(t *testing.T) (*Session, *Session, *rekeyTestTransport) {
	t.Helper()
	s := NewSession(&Config{}, nil)
	s.ctx = context.Background()
	s.SPIi, s.SPIr = 1, 2
	var err error
	s.EncAlg, err = crypto.GetEncrypterWithKeyLen(uint16(ikev2.ENCR_AES_CBC), 256)
	if err != nil {
		t.Fatal(err)
	}
	s.IntegAlg, err = crypto.GetIntegrityAlgorithm(uint16(ikev2.AUTH_HMAC_SHA2_256_128))
	if err != nil {
		t.Fatal(err)
	}
	s.PRFAlg, err = crypto.GetPRF(uint16(ikev2.PRF_HMAC_SHA1))
	if err != nil {
		t.Fatal(err)
	}
	s.ikeProposal = fixtureProposal(t, "aes256-sha256-prfsha1-modp2048", ikev2.ProtoIKE, nil)
	s.Keys = &ikev2.IKESAKeys{SK_d: bytes.Repeat([]byte{1}, 20), SK_ei: bytes.Repeat([]byte{2}, 32), SK_er: bytes.Repeat([]byte{3}, 32), SK_ai: bytes.Repeat([]byte{4}, 32), SK_ar: bytes.Repeat([]byte{5}, 32)}
	sock := &rekeyTestTransport{}
	s.socket = sock
	peer := NewSession(&Config{}, nil)
	peer.SPIi, peer.SPIr = 1, 2
	peer.Keys = s.Keys
	peer.EncAlg = s.EncAlg
	peer.IntegAlg = s.IntegAlg
	peer.PRFAlg = s.PRFAlg
	peer.localResponder = true
	return s, peer, sock
}
func passiveRequest(t *testing.T, s *Session) ([]ikev2.Payload, *crypto.DiffieHellman) {
	t.Helper()
	dh := newTestDH(t)
	return []ikev2.Payload{
		&ikev2.EncryptedPayloadSA{Proposals: []*ikev2.Proposal{copySAProposal(s.ikeProposal, 3, []byte{0, 0, 0, 0, 0, 0, 0, 3})}},
		&ikev2.EncryptedPayloadNonce{NonceData: bytes.Repeat([]byte{6}, 32)},
		&ikev2.EncryptedPayloadKE{DHGroup: ikev2.MODP_2048_bit, KEData: dh.PublicKeyBytes()},
	}, dh
}
func encryptTest(t *testing.T, s *Session, p []ikev2.Payload, x ikev2.ExchangeType, id uint32, response bool) []byte {
	t.Helper()
	b, err := s.encryptAndWrapWithMsgID(p, x, id, response)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func TestKHivePassiveRekeyEncryptedExchangeAndOldDelete(t *testing.T) {
	s, peer, sock := rekeyPair(t)
	oldKeys := s.Keys
	request, dh := passiveRequest(t, s)
	raw := encryptTest(t, peer, request, ikev2.CREATE_CHILD_SA, 7, false)
	done := make(chan struct{})
	go func() { s.dispatchCreateChildSA(raw); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("passive rekey deadlocked in dispatcher")
	}
	if len(sock.sent) != 1 || !s.localResponder || s.SPIi != 3 || s.Keys == oldKeys || s.SequenceNumber.Load() != 0 {
		t.Fatal("replacement SA not committed")
	}
	_, resp, err := peer.decryptAndParse(sock.sent[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := validateRekeyProposal(resp[0].(*ikev2.EncryptedPayloadSA), request[0].(*ikev2.EncryptedPayloadSA).Proposals[0], 8); err != nil {
		t.Fatal(err)
	}
	nr := resp[1].(*ikev2.EncryptedPayloadNonce).NonceData
	if _, err := dh.ComputeSharedSecret(resp[2].(*ikev2.EncryptedPayloadKE).KEData); err != nil {
		t.Fatal(err)
	}
	spi := binary.BigEndian.Uint64(resp[0].(*ikev2.EncryptedPayloadSA).Proposals[0].SPI)
	keys, err := peer.GenerateIKESARekeyKeys(oldKeys.SK_d, dh.SharedKey, request[1].(*ikev2.EncryptedPayloadNonce).NonceData, nr, 3, spi)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(keys.SK_ei, s.Keys.SK_ei) || !bytes.Equal(keys.SK_er, s.Keys.SK_er) {
		t.Fatal("peer derived different keys")
	}
	replacement := s.Keys
	// A duplicate request must replay exactly the old encrypted response.
	s.dispatchCreateChildSA(raw)
	if len(sock.sent) != 2 || !bytes.Equal(sock.sent[0], sock.sent[1]) || s.Keys != replacement {
		t.Fatal("duplicate rekey changed SA or response")
	}
	down := make(chan struct{}, 1)
	s.OnSessionDown = func() { down <- struct{}{} }
	del := []ikev2.Payload{&ikev2.EncryptedPayloadDelete{ProtocolID: ikev2.ProtoIKE}}
	rawDelete := encryptTest(t, peer, del, ikev2.INFORMATIONAL, 8, false)
	if !s.handleRetiredIKEPacket(rawDelete) {
		t.Fatal("old DELETE not handled")
	}
	_, empty, err := peer.decryptAndParse(sock.sent[2])
	if err != nil || len(empty) != 0 {
		t.Fatalf("old DELETE response: %v", err)
	}
	if s.Keys != replacement {
		t.Fatal("old DELETE changed replacement")
	}
	select {
	case <-down:
		t.Fatal("old DELETE disconnected new SA")
	default:
	}
	peer.SPIi, peer.SPIr, peer.Keys, peer.localResponder = 3, spi, keys, false
	// Both requests and responses must use the new role, flags and key direction.
	if err := s.handleIncomingInformational(encryptTest(t, peer, nil, ikev2.INFORMATIONAL, 0, false)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := peer.decryptAndParse(sock.sent[3]); err != nil {
		t.Fatal(err)
	}
	if _, _, err := peer.decryptAndParse(encryptTest(t, s, nil, ikev2.INFORMATIONAL, 0, false)); err != nil {
		t.Fatal(err)
	}
	// SKF uses the same new direction as SK.
	fragment, err := s.buildSKFPacket([]byte{1, 2, 3}, 1, 1, 1, ikev2.INFORMATIONAL, ikev2.NoNextPayload)
	if err != nil {
		t.Fatal(err)
	}
	plain, _, _, _, err := peer.decryptSKF(fragment)
	if err != nil || !bytes.Equal(plain, []byte{1, 2, 3}) {
		t.Fatalf("new-role SKF: %v", err)
	}
	// A forged packet cannot overwrite current SPIr before authentication.
	forged := encryptTest(t, peer, nil, ikev2.INFORMATIONAL, 2, false)
	forged[15] ^= 1
	if _, _, err := s.decryptAndParse(forged); err == nil || s.SPIr != spi {
		t.Fatal("unauthenticated SPI mutation")
	}
}

func TestKHivePassiveRekeyRejectsEncryptedInvalidOffers(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func([]ikev2.Payload)
		want   uint16
	}{
		{"key-length", func(p []ikev2.Payload) {
			p[0].(*ikev2.EncryptedPayloadSA).Proposals[0].Transforms[0].Attributes[0].Val = 128
		}, ikev2.NO_PROPOSAL_CHOSEN},
		{"missing-attribute", func(p []ikev2.Payload) { p[0].(*ikev2.EncryptedPayloadSA).Proposals[0].Transforms[0].Attributes = nil }, ikev2.NO_PROPOSAL_CHOSEN},
		{"zero-spi", func(p []ikev2.Payload) { p[0].(*ikev2.EncryptedPayloadSA).Proposals[0].SPI = make([]byte, 8) }, ikev2.NO_PROPOSAL_CHOSEN},
		{"short-spi", func(p []ikev2.Payload) { p[0].(*ikev2.EncryptedPayloadSA).Proposals[0].SPI = make([]byte, 4) }, ikev2.NO_PROPOSAL_CHOSEN},
		{"dh-group", func(p []ikev2.Payload) { p[2].(*ikev2.EncryptedPayloadKE).DHGroup = ikev2.MODP_1024_bit }, ikev2.INVALID_KE_PAYLOAD},
		{"short-ke", func(p []ikev2.Payload) { p[2].(*ikev2.EncryptedPayloadKE).KEData = []byte{2} }, ikev2.INVALID_SYNTAX},
		{"invalid-dh-public", func(p []ikev2.Payload) { p[2].(*ikev2.EncryptedPayloadKE).KEData = make([]byte, 256) }, ikev2.INVALID_SYNTAX},
		{"short-nonce", func(p []ikev2.Payload) { p[1].(*ikev2.EncryptedPayloadNonce).NonceData = []byte{1} }, ikev2.INVALID_SYNTAX},
		{"long-nonce", func(p []ikev2.Payload) { p[1].(*ikev2.EncryptedPayloadNonce).NonceData = make([]byte, 257) }, ikev2.INVALID_SYNTAX},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, peer, sock := rekeyPair(t)
			old := s.Keys
			p, _ := passiveRequest(t, s)
			tc.mutate(p)
			s.dispatchCreateChildSA(encryptTest(t, peer, p, ikev2.CREATE_CHILD_SA, 5, false))
			if s.Keys != old || s.SPIi != 1 || s.SPIr != 2 || s.localResponder || len(sock.sent) != 1 {
				t.Fatal("rejected request changed SA or lacked response")
			}
			_, ps, err := peer.decryptAndParse(sock.sent[0])
			if err != nil {
				t.Fatal(err)
			}
			if len(ps) != 1 || ps[0].(*ikev2.EncryptedPayloadNotify).NotifyType != tc.want {
				t.Fatalf("wrong rejection: %v", ps)
			}
		})
	}
}
func TestKHivePassiveRekeySendFailureAndCollision(t *testing.T) {
	s, _, sock := rekeyPair(t)
	old := s.Keys
	p, _ := passiveRequest(t, s)
	sock.fail = true
	if err := s.HandleRekeyIKESARequest(1, p); err == nil || s.Keys != old || len(s.retiredIKE) != 0 {
		t.Fatal("send failure committed replacement")
	}
	sock.fail = false
	s.rekeyMu.Lock()
	err := s.HandleRekeyIKESARequest(1, p)
	s.rekeyMu.Unlock()
	if err == nil || s.Keys != old || len(sock.sent) != 1 {
		t.Fatal("collision not rejected")
	}
}

func TestKHivePassiveRekeySelectsMatchingCandidateAndCanRekeyAgain(t *testing.T) {
	s, peer, sock := rekeyPair(t)
	request, _ := passiveRequest(t, s)
	sa := request[0].(*ikev2.EncryptedPayloadSA)
	rejected := copySAProposal(sa.Proposals[0], 1, sa.Proposals[0].SPI)
	rejected.Transforms[0].Attributes[0].Val = 128
	sa.Proposals = append([]*ikev2.Proposal{rejected}, sa.Proposals...)
	s.dispatchCreateChildSA(encryptTest(t, peer, request, ikev2.CREATE_CHILD_SA, 5, false))
	if len(sock.sent) != 1 || !s.localResponder {
		t.Fatal("matching later proposal not selected")
	}
	_, response, err := peer.decryptAndParse(sock.sent[0])
	if err != nil {
		t.Fatal(err)
	}
	if response[0].(*ikev2.EncryptedPayloadSA).Proposals[0].ProposalNum != 3 {
		t.Fatal("wrong proposal number")
	}
	peer.SPIi, peer.SPIr, peer.Keys, peer.localResponder = s.SPIi, s.SPIr, s.Keys, false
	oldKeys := s.Keys
	dh, peerDH := newTestDH(t), newTestDH(t)
	resp := []ikev2.Payload{
		&ikev2.EncryptedPayloadSA{Proposals: []*ikev2.Proposal{copySAProposal(s.ikeProposal, 1, []byte{0, 0, 0, 0, 0, 0, 0, 6})}},
		&ikev2.EncryptedPayloadNonce{NonceData: bytes.Repeat([]byte{8}, 32)},
		&ikev2.EncryptedPayloadKE{DHGroup: ikev2.MODP_2048_bit, KEData: peerDH.PublicKeyBytes()},
	}
	if err := s.handleRekeyIKESAResp(encryptTest(t, peer, resp, ikev2.CREATE_CHILD_SA, 0, true), bytes.Repeat([]byte{7}, 32), dh, 5, oldKeys.SK_d, s.SPIi, s.SPIr); err != nil {
		t.Fatal(err)
	}
	if s.localResponder || s.SPIi != 5 || s.SPIr != 6 {
		t.Fatal("active rekey failed to restore initiator role")
	}
	peer.SPIi, peer.SPIr, peer.Keys, peer.localResponder = s.SPIi, s.SPIr, s.Keys, true
	if _, _, err := peer.decryptAndParse(encryptTest(t, s, nil, ikev2.INFORMATIONAL, 0, false)); err != nil {
		t.Fatal(err)
	}
}
