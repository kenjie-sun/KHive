package db

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func TestKHiveTerminalReportsIsolationOrderingAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "terminal.db")
	if err := Init(path); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Truncate(time.Second)
	add := func(id, sim, peer string, parts int) {
		t.Helper()
		if err := CreateSMSDelivery(id, sim, "device", peer, "fixture", parts, now); err != nil {
			t.Fatal(err)
		}
		for n := 1; n <= parts; n++ {
			if err := UpsertSMSDeliveryPart(id, n, "", -1, "acked", now); err != nil {
				t.Fatal(err)
			}
		}
	}
	add("a", "sim-a", "+100", 2)
	add("b", "sim-b", "+100", 1)
	a0, err := ReserveSMSTerminalReference("a", 1)
	if err != nil {
		t.Fatal(err)
	}
	a1, err := ReserveSMSTerminalReference("a", 2)
	if err != nil || a0 == a1 {
		t.Fatalf("refs %d %d %v", a0, a1, err)
	}
	b0, err := ReserveSMSTerminalReference("b", 1)
	if err != nil || b0 != a0 {
		t.Fatalf("independent SIM refs %d %d %v", a0, b0, err)
	}
	sql, _ := DB.DB()
	_ = sql.Close()
	if err := Init(path); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sql, _ := DB.DB(); _ = sql.Close(); DB = nil })
	report := func(sim, peer string, mr, status int, delta time.Duration) {
		t.Helper()
		r := SMSStatusReport{IMSI: sim, Peer: peer, TPMR: mr, Status: status, ServiceCentreAt: now, DischargedAt: now.Add(delta)}
		if err := StoreSMSStatusReport(context.Background(), r, []byte(fmt.Sprintf("%s %s %d %d %v", sim, peer, mr, status, delta))); err != nil {
			t.Fatal(err)
		}
	}
	report("sim-a", "100", int(a0), 0, 0) // no suffix matching
	report("sim-a", "+100", int(a0), 0x20, time.Second)
	report("sim-a", "+100", int(a0), 0, 2*time.Second)
	report("sim-a", "+100", int(a0), 0, 2*time.Second)    // duplicate
	report("sim-a", "+100", int(a0), 0x20, 3*time.Second) // cannot regress final
	var p SMSDeliveryPart
	DB.First(&p, "message_id = ? AND part_no = 1", "a")
	if p.TerminalState != "delivered" || p.State != "acked" {
		t.Fatalf("terminal versus RP: %+v", p)
	}
	var other SMSDeliveryPart
	DB.First(&other, "message_id = ?", "b")
	if other.TerminalState != "pending" {
		t.Fatal("report crossed SIM")
	}
	var count int64
	DB.Model(&SMSStatusReport{}).Count(&count)
	if count != 4 {
		t.Fatalf("dedup count %d", count)
	}
	report("sim-a", "+100", int(a1), 0x40, time.Second)
	DB.First(&p, "message_id = ? AND part_no = 1", "a")
	status, err := GetSMSDeliveryStatus("a")
	if err != nil || len(status.Parts) != 2 || status.Parts[1].TerminalState != "failed" {
		t.Fatalf("multipart %+v %v", status, err)
	}
	// Early AT report is journaled, then reconciled using the modem's actual MR.
	report("sim-c", "+200", 99, 0, time.Second)
	add("c", "sim-c", "+200", 1)
	if err := AssociateATTerminalReference("c", 1, 99); err != nil {
		t.Fatal(err)
	}
	var c SMSDeliveryPart
	DB.First(&c, "message_id = ?", "c")
	if c.TerminalState != "delivered" {
		t.Fatalf("early report %+v", c)
	}
	// Reused AT references are deliberately ambiguous, including completed sends.
	add("d", "sim-c", "+200", 1)
	if err := AssociateATTerminalReference("d", 1, 99); err != nil {
		t.Fatal(err)
	}
	report("sim-c", "+200", 99, 0, 2*time.Second)
	var r SMSStatusReport
	DB.First(&r, "imsi = ? AND discharged_at = ?", "sim-c", now.Add(2*time.Second))
	if r.MatchState != "ambiguous" {
		t.Fatalf("reuse %+v", r)
	}
}
func TestKHiveTerminalStatusDoesNotOverclaimReceipt(t *testing.T) {
	for code, want := range map[int]string{0: "delivered", 1: "completed", 2: "completed", 0x20: "temporary_failure", 0x40: "failed", 0x60: "failed", 0x80: "unknown"} {
		if got := TerminalReportState(code); got != want {
			t.Fatalf("%x %s", code, got)
		}
	}
}
