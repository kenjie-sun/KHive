package swu

import (
	"errors"
	"github.com/1239t/swu-go/pkg/ikev2"
)

func (s *Session) ikeSenderFlags() uint8 {
	if s.localResponder {
		return 0
	}
	return ikev2.FlagInitiator
}
func (s *Session) outboundIKEKeys() ([]byte, []byte) {
	if s.localResponder {
		return s.Keys.SK_er, s.Keys.SK_ar
	}
	return s.Keys.SK_ei, s.Keys.SK_ai
}
func (s *Session) inboundIKEKeys() ([]byte, []byte) {
	if s.localResponder {
		return s.Keys.SK_ei, s.Keys.SK_ai
	}
	return s.Keys.SK_er, s.Keys.SK_ar
}
func (s *Session) validateEncryptedHeader(h *ikev2.IKEHeader) error {
	if s.Keys == nil || s.EncAlg == nil {
		return errors.New("IKE keys not established")
	}
	if h.SPIi != s.SPIi || h.SPIr != s.SPIr {
		return errors.New("encrypted packet belongs to another IKE SA")
	}
	if (h.Flags&ikev2.FlagInitiator != 0) != s.localResponder {
		return errors.New("invalid IKE sender role")
	}
	return nil
}
