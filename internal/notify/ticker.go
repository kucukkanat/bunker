package notify

import "time"

func newTicker() *time.Ticker { return time.NewTicker(25 * time.Second) }
