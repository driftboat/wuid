package internal

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/edwingeng/slog"
)

const (
	// PanicValue indicates when Next starts to panic
	PanicValue int64 = ((1 << 36) * 96 / 100) & ^1023
	// CriticalValue indicates when to renew the high 28 bits
	CriticalValue int64 = ((1 << 36) * 80 / 100) & ^1023
	// RenewIntervalMask indicates how often renew is performed if it fails
	RenewIntervalMask int64 = 0x20000000 - 1
)

const (
	H28Mask     = 0x07FFFFFF << 36
	L36Mask     = 0x0FFFFFFFFF
	SectionMask = 0x0FFFFFFFFFFFFFFF
)

type WUID struct {
	N     int64
	Step  int64
	Floor int64

	Monolithic bool
	Section    int64

	slog.Logger
	Name        string
	H28Verifier func(h28 int64) error

	sync.Mutex
	Renew func() error
}

func NewWUID(name string, logger slog.Logger, opts ...Option) (w *WUID) {
	w = &WUID{Step: 1, Name: name, Monolithic: true}
	if logger != nil {
		w.Logger = logger
	} else {
		w.Logger = slog.NewConsoleLogger()
	}
	for _, opt := range opts {
		opt(w)
	}
	return
}

func (w *WUID) Next() int64 {
	x := atomic.AddInt64(&w.N, w.Step)
	v := x & L36Mask
	if v >= PanicValue {
		panicValue := x&H28Mask | PanicValue
		atomic.CompareAndSwapInt64(&w.N, x, panicValue)
		panic(fmt.Errorf("<wuid> the low 36 bits are about to run out. name: %s", w.Name))
	}
	if v >= CriticalValue && v&RenewIntervalMask == 0 {
		go workerRenew(w)
	}
	if w.Floor == 0 {
		return x
	} else {
		return x / w.Floor * w.Floor
	}
}

func workerRenew(w *WUID) {
	defer func() {
		if r := recover(); r != nil {
			w.Warnf("<wuid> panic, renew failed. name: %s, reason: %+v", w.Name, r)
		}
	}()

	err := w.RenewNow()
	if err != nil {
		w.Warnf("<wuid> renew failed. name: %s, reason: %+v", w.Name, err)
	} else {
		w.Infof("<wuid> renew succeeded. name: %s", w.Name)
	}
}

func (w *WUID) RenewNow() error {
	w.Lock()
	f := w.Renew
	w.Unlock()
	return f()
}

func (w *WUID) Reset(n int64) {
	if n < 0 {
		panic("n cannot be negative")
	}
	if w.Monolithic {
		atomic.StoreInt64(&w.N, n)
	} else {
		v := n&SectionMask | w.Section
		atomic.StoreInt64(&w.N, v)
	}
}

func (w *WUID) VerifyH28(h28 int64) error {
	if h28 <= 0 {
		return errors.New("h28 must be positive. name: " + w.Name)
	}

	if w.Monolithic {
		if h28 > 0x07FFFFFF {
			return errors.New("h28 should not exceed 0x07FFFFFF. name: " + w.Name)
		}
	} else {
		if h28 > 0x00FFFFFF {
			return errors.New("h28 should not exceed 0x00FFFFFF. name: " + w.Name)
		}
	}

	current := atomic.LoadInt64(&w.N) >> 36
	if w.Monolithic {
		if h28 == current {
			return fmt.Errorf("h28 should be a different value other than %d. name: %s", h28, w.Name)
		}
	} else {
		if h28 == current&0x00FFFFFF {
			return fmt.Errorf("h28 should be a different value other than %d. name: %s", h28, w.Name)
		}
	}

	if w.H28Verifier != nil {
		if err := w.H28Verifier(h28); err != nil {
			return err
		}
	}

	return nil
}

type Option func(*WUID)

func WithSection(section int8) Option {
	if section < 0 || section > 7 {
		panic("section must be in between [0, 7]")
	}
	return func(w *WUID) {
		w.Monolithic = false
		w.Section = int64(section) << 60
	}
}

func WithH28Verifier(cb func(h28 int64) error) Option {
	return func(w *WUID) {
		w.H28Verifier = cb
	}
}

func WithStep(step int64, floor int64) Option {
	switch step {
	case 1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024:
	default:
		panic("the step must be one of these values: 1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024")
	}
	if floor != 0 && (floor < 0 || floor >= step) {
		panic(fmt.Errorf("floor must be in between [0, %d)", step))
	}
	return func(wuid *WUID) {
		wuid.Step = step
		wuid.Floor = floor
	}
}
