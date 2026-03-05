// Обёртка Ticker для периодической проверки здоровья.
package kms

import "time"

// Ticker wraps time.Ticker for easier testing
type Ticker struct {
	*time.Ticker
	C <-chan time.Time
}

// NewTicker creates a new Ticker
func NewTicker(d time.Duration) *Ticker {
	t := time.NewTicker(d)
	return &Ticker{
		Ticker: t,
		C:      t.C,
	}
}
