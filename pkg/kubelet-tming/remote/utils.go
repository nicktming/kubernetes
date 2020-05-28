package remote

import (
	"context"
	"time"
)

const maxMsgSize = 1024 * 1024 * 16

func getContextWithTimeout(timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), timeout)
}
