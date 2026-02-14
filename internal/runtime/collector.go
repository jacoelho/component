package runtime

import (
	"errors"
	"fmt"
	"sync"
)

// ErrorCollector safely aggregates errors from concurrent operations.
type ErrorCollector struct {
	mu   sync.Mutex
	errs []error
}

func NewErrorCollector() *ErrorCollector {
	return &ErrorCollector{errs: make([]error, 0)}
}

func (ec *ErrorCollector) Add(err error) {
	if err == nil {
		return
	}
	ec.mu.Lock()
	ec.errs = append(ec.errs, err)
	ec.mu.Unlock()
}

func (ec *ErrorCollector) Addf(format string, args ...any) {
	ec.Add(fmt.Errorf(format, args...))
}

func (ec *ErrorCollector) Err() error {
	ec.mu.Lock()
	defer ec.mu.Unlock()
	if len(ec.errs) == 0 {
		return nil
	}
	return errors.Join(ec.errs...)
}
