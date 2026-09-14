package device

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

func (w *Worker) persistIncomingPDUHex(raw string) error {
	imsi := w.GetCachedIMSI()
	if imsi == "" {
		imsi = w.GetIMSI()
	}
	return w.persistIncomingPDUHexForIMSI(raw, imsi)
}

func (w *Worker) snapshotSMSReportCallback() func(string) error {
	imsi := w.GetCachedIMSI()
	return func(raw string) error {
		if imsi == "" {
			return fmt.Errorf("SMS report received before SIM identity ready")
		}
		return w.persistIncomingPDUHexForIMSI(raw, imsi)
	}
}

func (w *Worker) persistIncomingPDUHexForIMSI(raw, imsi string) error {
	bytes, err := hex.DecodeString(strings.TrimSpace(raw))
	if err != nil {
		return err
	}
	if len(bytes) < 2 || int(bytes[0])+1 >= len(bytes) {
		return fmt.Errorf("invalid SMSC/PDU length")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return persistIncomingTPDU(ctx, w.Pool, w.ID, imsi, bytes[1+int(bytes[0]):], w.smsMode.String())
}
