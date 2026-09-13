package device

import "context"

// Test-only bridge lets the external integration test attach a real notification
// manager without introducing a production import cycle (notify imports device).
func KHiveIncomingStoreForTest(pool *Pool) interface {
	ReceiveSMS(context.Context, string, string, []byte) ([]byte, error)
} {
	return vowifiDeliveryStore{pool: pool}
}
