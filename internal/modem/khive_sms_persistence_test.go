package modem

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

const khiveSMSFixture = "0000049121430008629021214365000400480069"

func TestKHiveATPersistenceFailureDoesNotDelete(t *testing.T) {
	m := newRunningTestManager(t)
	m.SetPDUCallback(func(string) error { return errors.New("storage unavailable") })
	commands := make(chan []string, 1)
	go func() {
		commands <- respondToCommands(t, m, 1, func(req commandRequest) { req.respChan <- "+CMGR: 0,,19\r\n" + khiveSMSFixture + "\r\nOK\r\n" })
	}()
	done := make(chan struct{})
	go func() { m.ReadAndProcessSMS("7"); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("read attempted a delete after storage failure")
	}
	if got := <-commands; !reflect.DeepEqual(got, []string{"AT+CMGR=7"}) {
		t.Fatal(got)
	}
}
func TestKHiveATPollingDeletesOnlyPersistedIndices(t *testing.T) {
	m := newRunningTestManager(t)
	calls := 0
	m.SetPDUCallback(func(string) error { calls++; return nil })
	commands := make(chan []string, 1)
	go func() {
		commands <- respondToCommands(t, m, 3, func(req commandRequest) {
			if req.cmd == "AT+CMGL=4" {
				req.respChan <- "+CMGL: 2,0,,19\r\n" + khiveSMSFixture + "\r\n+CMGL: 7,0,,19\r\n" + khiveSMSFixture + "\r\nOK\r\n"
			} else {
				req.respChan <- "OK"
			}
		})
	}()
	done := make(chan struct{})
	go func() { m.CheckAllSMS(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("SMS poll blocked")
	}
	if got := <-commands; !reflect.DeepEqual(got, []string{"AT+CMGL=4", "AT+CMGD=2", "AT+CMGD=7"}) || calls != 2 {
		t.Fatalf("commands=%v saves=%d", got, calls)
	}
}
