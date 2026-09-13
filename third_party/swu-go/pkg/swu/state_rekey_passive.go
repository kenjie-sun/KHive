package swu

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	"github.com/1239t/swu-go/pkg/crypto"
	"github.com/1239t/swu-go/pkg/ikev2"
)

// Retain only bounded old-SA crypto and authenticated exchange state. The peer
// deletes the old SA after rekey, and a lost response may cause retransmission.
// Neither event may delete the replacement SA or derive another set of keys.
type retiredIKESA struct {
	codec       *Session
	expires     time.Time
	requestID   uint32
	requestHash [32]byte
	response    []byte
}

func payloadDigest(payloads []ikev2.Payload) ([32]byte, error) {
	h := sha256.New()
	for _, p := range payloads {
		b, err := p.Encode()
		if err != nil {
			return [32]byte{}, err
		}
		h.Write([]byte{byte(p.Type())})
		size := make([]byte, 4)
		binary.BigEndian.PutUint32(size, uint32(len(b)))
		h.Write(size)
		h.Write(b)
	}
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return sum, nil
}

// Preserve the negotiated suite, including all attributes. Different proposal
// numbers and multiple candidate proposals are legal in a new request.
func selectIKERekeyProposal(sa *ikev2.EncryptedPayloadSA, selected *ikev2.Proposal) (*ikev2.Proposal, error) {
	if sa == nil || selected == nil || selected.ProtocolID != ikev2.ProtoIKE {
		return nil, errors.New("missing negotiated IKE proposal")
	}
	for _, p := range sa.Proposals {
		if p == nil || p.ProposalNum == 0 {
			continue
		}
		expected := copySAProposal(selected, p.ProposalNum, nil)
		if validateRekeyProposal(&ikev2.EncryptedPayloadSA{Proposals: []*ikev2.Proposal{p}}, expected, 8) == nil {
			return p, nil
		}
	}
	return nil, errors.New("no matching IKE rekey proposal")
}

func (s *Session) rejectIKERekey(msgID uint32, kind uint16, detail []byte, reason error) error {
	n := &ikev2.EncryptedPayloadNotify{NotifyType: kind, NotifyData: detail}
	if err := s.sendEncryptedResponseWithMsgID([]ikev2.Payload{n}, ikev2.CREATE_CHILD_SA, msgID); err != nil {
		return fmt.Errorf("%v: sending rejection: %w", reason, err)
	}
	return reason
}

func (s *Session) HandleRekeyIKESARequest(msgID uint32, payloads []ikev2.Payload) error {
	if !s.rekeyMu.TryLock() {
		return s.rejectIKERekey(msgID, ikev2.TEMPORARY_FAILURE, nil, errors.New("rekey already in progress"))
	}
	defer s.rekeyMu.Unlock()
	return s.handleRekeyIKESARequestLocked(msgID, payloads)
}

// The dispatcher already holds rekeyMu. Do not recursively acquire it here.
func (s *Session) handleRekeyIKESARequestLocked(msgID uint32, payloads []ikev2.Payload) error {
	if s.Keys == nil || len(s.Keys.SK_d) == 0 || s.PRFAlg == nil {
		return errors.New("IKE keys not established")
	}
	var sa *ikev2.EncryptedPayloadSA
	var nonce *ikev2.EncryptedPayloadNonce
	var ke *ikev2.EncryptedPayloadKE
	invalid := false
	for _, p := range payloads {
		switch v := p.(type) {
		case *ikev2.EncryptedPayloadSA:
			if sa != nil {
				invalid = true
			}
			sa = v
		case *ikev2.EncryptedPayloadNonce:
			if nonce != nil {
				invalid = true
			}
			nonce = v
		case *ikev2.EncryptedPayloadKE:
			if ke != nil {
				invalid = true
			}
			ke = v
		case *ikev2.EncryptedPayloadTS:
			invalid = true
		}
	}
	if invalid || sa == nil || nonce == nil || ke == nil || len(nonce.NonceData) < 16 || len(nonce.NonceData) > 256 {
		return s.rejectIKERekey(msgID, ikev2.INVALID_SYNTAX, nil, errors.New("invalid IKE rekey payload set or nonce"))
	}
	proposal, err := selectIKERekeyProposal(sa, s.ikeProposal)
	if err != nil {
		return s.rejectIKERekey(msgID, ikev2.NO_PROPOSAL_CHOSEN, nil, err)
	}
	if ke.DHGroup != ikev2.MODP_2048_bit {
		return s.rejectIKERekey(msgID, ikev2.INVALID_KE_PAYLOAD, []byte{0, 14}, errors.New("IKE rekey requires negotiated DH group 14"))
	}
	if len(ke.KEData) != 256 {
		return s.rejectIKERekey(msgID, ikev2.INVALID_SYNTAX, nil, errors.New("invalid MODP public key length"))
	}
	dh, err := crypto.NewDiffieHellman(14)
	if err != nil {
		return err
	}
	if err = dh.GenerateKey(); err != nil {
		return err
	}
	if _, err = dh.ComputeSharedSecret(ke.KEData); err != nil {
		return s.rejectIKERekey(msgID, ikev2.INVALID_SYNTAX, nil, err)
	}
	nr, err := crypto.RandomBytes(32)
	if err != nil {
		return err
	}
	spi, err := crypto.RandomBytes(8)
	if err != nil {
		return err
	}
	peerSPI, localSPI := binary.BigEndian.Uint64(proposal.SPI), binary.BigEndian.Uint64(spi)
	if localSPI == 0 {
		return errors.New("random IKE SPI is zero")
	}
	keys, err := s.GenerateIKESARekeyKeys(s.Keys.SK_d, dh.SharedKey, nonce.NonceData, nr, peerSPI, localSPI)
	if err != nil {
		return err
	}
	digest, err := payloadDigest(payloads)
	if err != nil {
		return err
	}
	response := []ikev2.Payload{
		&ikev2.EncryptedPayloadSA{Proposals: []*ikev2.Proposal{copySAProposal(proposal, proposal.ProposalNum, spi)}},
		&ikev2.EncryptedPayloadNonce{NonceData: nr},
		&ikev2.EncryptedPayloadKE{DHGroup: ikev2.MODP_2048_bit, KEData: dh.PublicKeyBytes()},
	}
	raw, err := s.encryptAndWrapWithMsgID(response, ikev2.CREATE_CHILD_SA, msgID, true)
	if err != nil {
		return err
	}
	// Derive and encode everything before telling the peer to switch. A send
	// failure leaves the old SA usable; no state is committed on rejection.
	if err = s.socket.SendIKE(raw); err != nil {
		return err
	}
	old := &Session{SPIi: s.SPIi, SPIr: s.SPIr, Keys: s.Keys, EncAlg: s.EncAlg, IntegAlg: s.IntegAlg, PRFAlg: s.PRFAlg, ikeIsAEAD: s.ikeIsAEAD, localResponder: s.localResponder, Logger: s.Logger, fragmentBuf: newFragmentBuffer()}
	s.retiredMu.Lock()
	retained := s.retiredIKE[:0]
	for _, r := range s.retiredIKE {
		if time.Now().Before(r.expires) {
			retained = append(retained, r)
		}
	}
	if len(retained) >= 4 {
		retained = retained[len(retained)-3:]
	}
	s.retiredIKE = append(retained, &retiredIKESA{codec: old, expires: time.Now().Add(2 * time.Minute), requestID: msgID, requestHash: digest, response: raw})
	s.retiredMu.Unlock()
	s.ikeStateMu.Lock()
	s.SPIi, s.SPIr, s.Keys, s.DH = peerSPI, localSPI, keys, dh
	s.localResponder = true
	s.SequenceNumber.Store(0)
	s.fragmentBuf = newFragmentBuffer()
	s.ikeStateMu.Unlock()
	s.lastRekeyTime = time.Now()
	s.Logger.Info("被动 IKE SA Rekey 完成，完整协商参数与 responder 密钥方向已保留")
	select {
	case s.rekeyResetCh <- struct{}{}:
	default:
	}
	return nil
}

func (s *Session) handleRetiredIKEPacket(data []byte) bool {
	h, err := ikev2.DecodeHeader(data)
	if err != nil {
		return false
	}
	s.retiredMu.Lock()
	defer s.retiredMu.Unlock()
	var old *retiredIKESA
	for _, r := range s.retiredIKE {
		if h.SPIi == r.codec.SPIi && h.SPIr == r.codec.SPIr {
			old = r
			break
		}
	}
	if old == nil {
		return false
	}
	if time.Now().After(old.expires) || h.Flags&ikev2.FlagResponse != 0 || h.NextPayload != ikev2.SK {
		return true
	}
	_, payloads, err := old.codec.decryptAndParse(data)
	if err != nil {
		return true
	}
	if h.ExchangeType == ikev2.CREATE_CHILD_SA && h.MessageID == old.requestID {
		digest, err := payloadDigest(payloads)
		if err == nil && digest == old.requestHash {
			if err = s.socket.SendIKE(old.response); err != nil {
				s.Logger.Warn("旧 IKE rekey 响应重传失败")
			}
		}
		return true
	}
	if h.ExchangeType == ikev2.INFORMATIONAL && len(payloads) == 1 && h.MessageID == old.requestID+1 {
		if del, ok := payloads[0].(*ikev2.EncryptedPayloadDelete); ok && del.ProtocolID == ikev2.ProtoIKE && del.SPISize == 0 && del.NumSPIs == 0 && len(del.SPIs) == 0 {
			raw, err := old.codec.encryptAndWrapWithMsgID(nil, ikev2.INFORMATIONAL, h.MessageID, true)
			if err == nil {
				if err = s.socket.SendIKE(raw); err != nil {
					s.Logger.Warn("旧 IKE DELETE 确认发送失败")
				}
			}
		}
	}
	return true
}
