// Обёртка над time.Ticker: единый тип с экспортируемым каналом C для моков в тестах.
package kms

import "time"

// Ticker — тонкая оболочка над стандартным тикером; поле C дублирует t.C для стабильного API пакета.
type Ticker struct {
	*time.Ticker
	C <-chan time.Time
}

// NewTicker запускает периодический тик с интервалом d; вызывающий обязан вызвать Stop при завершении.
func NewTicker(d time.Duration) *Ticker {
	t := time.NewTicker(d)
	return &Ticker{
		Ticker: t,
		C:      t.C,
	}
}
