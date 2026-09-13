package esim

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/damonto/euicc-go/bertlv"
	"github.com/damonto/euicc-go/lpa"
	sgp22 "github.com/damonto/euicc-go/v2"
)

func TestKHiveDownloadRecoveryCleanupFailureKeepsInstalledResult(t *testing.T) {
	iccid, _ := sgp22.NewICCID("8986001234567890123")
	pending := testPendingNotification(12, sgp22.NotificationEventInstall, iccid, "install.example.com")
	client, rt, tx := newTestNotificationClientWithTransmitter([]*sgp22.NotificationMetadata{pending.Notification}, map[sgp22.SequenceNumber][]*sgp22.PendingNotification{12: {pending}}, nil, map[sgp22.SequenceNumber]error{12: errors.New("cleanup unavailable")}, nil)
	m := NewManagerWithChannelFactory("test", func([]byte) (*lpa.Client, error) { return client, nil }, nil, nil, nil)
	m.closeClient = func(*lpa.Client) error { return nil }
	result, recovered := m.recoverDownloadInstallFinalizeError(context.Background(), []byte{1}, []*sgp22.NotificationMetadata{{SequenceNumber: 11}}, errors.New("APDU response lost"))
	if !recovered || result.WarningCode != "download_notification_cleanup_failed" || len(rt.handledHosts) != 1 || len(tx.removed) != 0 {
		t.Fatalf("result=%+v recovered=%v sends=%v removed=%v", result, recovered, rt.handledHosts, tx.removed)
	}
}

func TestKHiveNotificationFinalizePreservesUndeliveredAndUnrelatedRecords(t *testing.T) {
	iccid, _ := sgp22.NewICCID("8986001234567890123")
	for _, mode := range []string{"success", "send_failure", "cleanup_failure", "empty", "nil", "mismatched", "already_removed"} {
		t.Run(mode, func(t *testing.T) {
			pending := []*sgp22.PendingNotification{testPendingNotification(12, sgp22.NotificationEventInstall, iccid, "install.example.com")}
			removeErr := map[sgp22.SequenceNumber]error{}
			sendErr := map[string]error{}
			switch mode {
			case "send_failure":
				sendErr["install.example.com"] = errors.New("offline")
			case "cleanup_failure":
				removeErr[12] = errors.New("card error")
			case "empty":
				pending = nil
			case "nil":
				pending = []*sgp22.PendingNotification{nil}
			case "mismatched":
				pending[0].Notification.SequenceNumber = 13
			case "already_removed":
				removeErr[12] = sgp22.ErrNothingToDelete
			}
			client, rt, tx := newTestNotificationClientWithTransmitter([]*sgp22.NotificationMetadata{{SequenceNumber: 11}, {SequenceNumber: 12}}, nil, nil, removeErr, sendErr)
			err := sendAndRemoveNotification(client, 12, pending, 0)
			if mode == "success" {
				if err != nil || fmt.Sprint(tx.removed) != "[12]" || len(tx.list) != 1 || tx.list[0].SequenceNumber != 11 || len(rt.handledHosts) != 1 {
					t.Fatalf("err=%v removed=%v sends=%v", err, tx.removed, rt.handledHosts)
				}
			} else if mode == "already_removed" {
				if err != nil || len(rt.handledHosts) != 1 {
					t.Fatalf("err=%v sends=%v", err, rt.handledHosts)
				}
			} else {
				if err == nil || len(tx.removed) != 0 {
					t.Fatalf("err=%v removed=%v", err, tx.removed)
				}
				if mode == "cleanup_failure" {
					var cleanup *notificationCleanupError
					if !errors.As(err, &cleanup) || len(rt.handledHosts) != 1 {
						t.Fatalf("cleanup resent notification: %v %v", err, rt.handledHosts)
					}
					if downloadNotificationResult(true, nil, err).WarningCode != "download_notification_cleanup_failed" || deleteNotificationResult(true, nil, err).WarningCode != "delete_notification_cleanup_failed" {
						t.Fatal("cleanup incorrectly reported as delivery failure")
					}
				} else if mode != "send_failure" && len(rt.handledHosts) != 0 {
					t.Fatal("invalid notification sent")
				}
			}
		})
	}
}

type deleteRecoveryTransmitter struct {
	*fakeNotificationTransmitter
	target         sgp22.ICCID
	present        bool
	deleteCalls    int
	deleteErr      error
	leavePresent   bool
	noNotification bool
	profileReadErr error
}

func (f *deleteRecoveryTransmitter) Transmit(request bertlv.Marshaler, response bertlv.Unmarshaler) error {
	switch request.(type) {
	case *sgp22.ProfileInfoListRequest:
		if f.profileReadErr != nil {
			return f.profileReadErr
		}
		r := response.(*sgp22.ProfileInfoListResponse)
		if f.present {
			r.ProfileList = []*sgp22.ProfileInfo{{ICCID: f.target, ProfileState: sgp22.ProfileDisabled}}
		}
		return nil
	case *sgp22.ProfileOperationRequest:
		f.deleteCalls++
		f.present = f.leavePresent
		if !f.noNotification {
			f.list = append(f.list, &sgp22.NotificationMetadata{SequenceNumber: 12, ProfileManagementOperation: sgp22.NotificationEventDelete, ICCID: f.target, Address: "delete.example.com"})
		}
		return f.deleteErr
	default:
		return f.fakeNotificationTransmitter.Transmit(request, response)
	}
}

func TestKHiveDeleteAPDUErrorRecoversWithoutRepeatingDeletion(t *testing.T) {
	for _, mode := range []string{"normal", "committed_error", "still_present", "no_notification", "reopen_failure"} {
		t.Run(mode, func(t *testing.T) {
			iccid, _ := sgp22.NewICCID("8986001234567890123")
			client, rt, notifications := newTestNotificationClientWithTransmitter([]*sgp22.NotificationMetadata{{SequenceNumber: 11}}, map[sgp22.SequenceNumber][]*sgp22.PendingNotification{12: {testPendingNotification(12, sgp22.NotificationEventDelete, iccid, "delete.example.com")}}, nil, nil, nil)
			tx := &deleteRecoveryTransmitter{fakeNotificationTransmitter: notifications, target: iccid, present: true, deleteErr: errors.New("APDU response lost"), leavePresent: mode == "still_present", noNotification: mode == "no_notification"}
			if mode == "normal" {
				tx.deleteErr = nil
			}
			client.APDU = tx
			opens, closes := 0, 0
			m := NewManagerWithChannelFactory("test", func([]byte) (*lpa.Client, error) {
				opens++
				if opens > 1 && mode == "reopen_failure" {
					return nil, errors.New("offline")
				}
				return client, nil
			}, nil, nil, nil)
			m.closeClient = func(*lpa.Client) error { closes++; return nil }
			result, err := m.DeleteProfile(iccid.String(), "A0000005591010")
			if tx.deleteCalls != 1 {
				t.Fatalf("delete calls=%d", tx.deleteCalls)
			}
			if mode == "normal" || mode == "committed_error" {
				if err != nil || fmt.Sprint(notifications.removed) != "[12]" || len(rt.handledHosts) != 1 {
					t.Fatalf("result=%+v err=%v removed=%v", result, err, notifications.removed)
				}
				if mode == "committed_error" && (result.WarningCode != "delete_apdu_error_recovered" || opens != 2 || closes != 2) {
					t.Fatalf("result=%+v opens=%d closes=%d", result, opens, closes)
				}
			} else if err == nil || len(notifications.removed) != 0 || len(rt.handledHosts) != 0 {
				t.Fatalf("unconfirmed deletion reported success: %+v %v", result, err)
			}
		})
	}
}

func TestKHiveDeleteRecoveryRequiresFreshMatchingDeleteEvidence(t *testing.T) {
	target, _ := sgp22.NewICCID("8986001234567890123")
	other, _ := sgp22.NewICCID("8986001234567890124")
	for _, mode := range []string{"fresh", "old", "wrong_iccid", "wrong_event", "read_failure"} {
		t.Run(mode, func(t *testing.T) {
			n := &sgp22.NotificationMetadata{SequenceNumber: 12, ICCID: target, ProfileManagementOperation: sgp22.NotificationEventDelete}
			tx := &deleteRecoveryTransmitter{fakeNotificationTransmitter: &fakeNotificationTransmitter{list: []*sgp22.NotificationMetadata{n}}}
			switch mode {
			case "old":
				n.SequenceNumber = 11
			case "wrong_iccid":
				n.ICCID = other
			case "wrong_event":
				n.ProfileManagementOperation = sgp22.NotificationEventInstall
			case "read_failure":
				tx.profileReadErr = errors.New("offline")
			}
			err := verifyDeletedProfile(&lpa.Client{APDU: tx}, target, 11)
			if (err == nil) != (mode == "fresh") {
				t.Fatalf("err=%v", err)
			}
		})
	}
}
