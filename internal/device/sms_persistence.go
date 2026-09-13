package device

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

func (w *Worker) persistIncomingPDUHex(raw string) error {
	bytes, err := hex.DecodeString(strings.TrimSpace(raw))
	if err != nil {
		return err
	}
	if len(bytes) < 2 || int(bytes[0])+1 >= len(bytes) {
		return fmt.Errorf("invalid SMSC/PDU length")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	imsi := w.GetCachedIMSI()
	if imsi == "" {
		imsi = w.GetIMSI()
	}
	return persistIncomingTPDU(ctx, w.Pool, w.ID, imsi, bytes[1+int(bytes[0]):], w.smsMode.String())
}
