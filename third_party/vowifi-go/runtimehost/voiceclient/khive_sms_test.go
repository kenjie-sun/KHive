package voiceclient

import (
	"errors"
	"testing"
	"time"

	"github.com/1239t/vowifi-go/runtimehost/messaging"
	"github.com/emiago/sipgo/sip"
)

type khiveSMSStore struct {
	messaging.DeliveryStore
	err    error
	called bool
}

func (s *khiveSMSStore) MarkSMSDeliveryPartReport(a, b, c string, mr int, state string, code, cause int, msg string, at time.Time) (messaging.DeliveryPartMatch, error) {
	s.called = true
	return messaging.DeliveryPartMatch{}, s.err
}

func (s *khiveSMSStore) RecomputeSMSDelivery(string, time.Time) error { return nil }

type khiveSMSTransaction struct {
	sip.ServerTransaction
	response *sip.Response
}

func (tx *khiveSMSTransaction) Respond(r *sip.Response) error { tx.response = r; return nil }

func TestKHiveIncomingSMSDoesNotAcknowledgeUnprocessedData(t *testing.T) {
	for _, tc := range []struct {
		name, contentType string
		body              []byte
		fail              bool
		code              int
		called            bool
	}{
		{"inbound_not_wired", smsContentType, []byte{1, 42, 0, 0}, false, 503, false},
		{"invalid_body", smsContentType, []byte{3}, false, 503, false},
		{"bad_type", "text/plain", []byte{3, 42}, false, 415, false},
		{"report_with_parameters", smsContentType + "; charset=binary", []byte{3, 42}, false, 200, true},
		{"storage_failure", smsContentType, []byte{3, 42}, true, 503, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &khiveSMSStore{}
			if tc.fail {
				store.err = errors.New("storage unavailable")
			}
			c := &Client{cfg: Config{DeliveryStore: store}}
			req := sip.NewRequest(sip.MESSAGE, sip.Uri{Scheme: "sip", Host: "example.invalid"})
			req.AppendHeader(sip.NewHeader("Content-Type", tc.contentType))
			req.SetBody(tc.body)
			tx := &khiveSMSTransaction{}
			c.handleIncomingMessage(req, tx)
			if tx.response == nil || tx.response.StatusCode != tc.code || store.called != tc.called {
				t.Fatalf("response=%v storeCalled=%v", tx.response, store.called)
			}
		})
	}
}
