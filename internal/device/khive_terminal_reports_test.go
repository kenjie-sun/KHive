package device

import (
	"context"
	"encoding/hex"
	"github.com/1239t/vohive/internal/db"
	"github.com/1239t/vohive/pkg/smscodec"
	"github.com/warthog618/sms/encoding/tpdu"
	"testing"
	"time"
)

func TestKHiveATDirectReportSnapshotsSIMAndReconcilesEarlyReport(t *testing.T) {
	initDevicePhoneNumberTestDB(t)
	now := time.Now().Truncate(time.Second)
	for _, item := range []struct{ id, imsi string }{{"direct-a", "SIM-A"}, {"direct-b", "SIM-B"}} {
		if err := db.CreateSMSDelivery(item.id, item.imsi, "device", "+1234", "text", 1, now); err != nil {
			t.Fatal(err)
		}
		if err := db.UpsertSMSDeliveryPart(item.id, 1, "", -1, "acked", now); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.AssociateATTerminalReference("direct-b", 1, 42); err != nil {
		t.Fatal(err)
	}
	w := &Worker{ID: "device", smsMode: smsModeAT}
	w.state.Identity.IMSI = "SIM-A"
	persist := w.snapshotSMSReportCallback()
	// The callback must retain the original identity even if the worker changes
	// profiles before the asynchronous persistence job gets CPU time.
	w.state.Identity.IMSI = "SIM-B"
	r := tpdu.TPDU{Direction: tpdu.MT, FirstOctet: 2, MR: 42, RA: tpdu.Address{Addr: "1234", TOA: 0x91}, SCTS: tpdu.Timestamp{Time: now}, DT: tpdu.Timestamp{Time: now}, ST: 0}
	raw, err := r.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if err := persist(hex.EncodeToString(append([]byte{0}, raw...))); err != nil {
		t.Fatal(err)
	}
	if err := db.AssociateATTerminalReference("direct-a", 1, 42); err != nil {
		t.Fatal(err)
	}
	a, err := db.GetSMSDeliveryStatus("direct-a")
	if err != nil {
		t.Fatal(err)
	}
	b, err := db.GetSMSDeliveryStatus("direct-b")
	if err != nil {
		t.Fatal(err)
	}
	if a.Parts[0].TerminalState != "delivered" || b.Parts[0].TerminalState != "pending" {
		t.Fatalf("cross-SIM report: a=%+v b=%+v", a, b)
	}
	w.state.Identity.IMSI = ""
	if err := w.snapshotSMSReportCallback()(hex.EncodeToString(append([]byte{0}, raw...))); err == nil {
		t.Fatal("report with no receiving SIM accepted")
	}
}

func TestKHiveTerminalReportPersistsBeforeRPAck(t *testing.T) {
	initDevicePhoneNumberTestDB(t)
	now := time.Now().Truncate(time.Second)
	if err := db.CreateSMSDelivery("terminal", "SIM-A", "device", "+1234", "text", 1, now); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertSMSDeliveryPart("terminal", 1, "call", 7, "acked", now); err != nil {
		t.Fatal(err)
	}
	parts, _, err := smscodec.BuildSubmitTPDUs("+1234", "text")
	if err != nil {
		t.Fatal(err)
	}
	store := vowifiDeliveryStore{}
	body, err := store.PrepareSMSTerminalSubmit("terminal", 1, smscodec.BuildRPData(7, parts[0], "+999"))
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, submitted, err := smscodec.ParseRPDataWithAddresses(body)
	if err != nil || submitted[0]&0x20 == 0 {
		t.Fatalf("TP-SRR absent %x %v", submitted, err)
	}
	report := tpdu.TPDU{Direction: tpdu.MT, FirstOctet: 2, MR: submitted[1], RA: tpdu.Address{Addr: "1234", TOA: 0x91}, SCTS: tpdu.Timestamp{Time: now}, DT: tpdu.Timestamp{Time: now}, ST: 0}
	raw, err := report.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	rp := append([]byte{1, 88, 0, 0, byte(len(raw))}, raw...)
	if err := db.DB.Exec("CREATE TRIGGER fail_report BEFORE INSERT ON sms_status_reports BEGIN SELECT RAISE(ABORT,'fixture'); END").Error; err != nil {
		t.Fatal(err)
	}
	if ack, err := store.ReceiveSMS(context.Background(), "device", "SIM-A", rp); err == nil || ack != nil {
		t.Fatal("acknowledged report without persistence")
	}
	_ = db.DB.Exec("DROP TRIGGER fail_report").Error
	ack, err := store.ReceiveSMS(context.Background(), "device", "SIM-A", rp)
	if err != nil || len(ack) != 2 || ack[0] != 2 || ack[1] != 88 {
		t.Fatalf("ack %x %v", ack, err)
	}
	status, err := db.GetSMSDeliveryStatus("terminal")
	if err != nil || status.Parts[0].TerminalState != "delivered" || status.Parts[0].State != "acked" {
		t.Fatalf("status %+v %v", status, err)
	}
	var count int64
	db.DB.Model(&db.SMS{}).Count(&count)
	if count != 0 {
		t.Fatal("status report became an incoming text message")
	}
}
