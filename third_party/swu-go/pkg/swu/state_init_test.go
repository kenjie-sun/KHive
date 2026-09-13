package swu

import (
	"github.com/1239t/swu-go/pkg/ikev2"
	"strings"
	"testing"
)

func TestInvalidKENotificationReportsRequestedGroup(t *testing.T) {
	p := ikev2.NewIKEPacket()
	p.Header.Version = 0x20
	p.Header.ExchangeType = ikev2.IKE_SA_INIT
	p.Payloads = []ikev2.Payload{&ikev2.EncryptedPayloadNotify{NotifyType: 17, NotifyData: []byte{0, 2}}}
	raw, err := p.Encode()
	if err != nil {
		t.Fatal(err)
	}
	s := &Session{}
	err = s.handleIKESAInitResp(raw)
	if err == nil || !strings.Contains(err.Error(), "DH group 2 (INVALID_KE_PAYLOAD)") {
		t.Fatalf("incorrect error: %v", err)
	}
}
