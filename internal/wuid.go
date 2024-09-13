package internal

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/edwingeng/slog"
)

const (
	// PanicValue indicates when Next starts to panic.
	PanicValue int64 = ((1 << 32) * 96 / 100) & ^1023
	// CriticalValue indicates when to renew the high 28 bits.
	CriticalValue int64 = ((1 << 32) * 80 / 100) & ^1023
	// RenewIntervalMask indicates the 'time' between two renewal attempts.
	RenewIntervalMask int64 = 0x2000000 - 1
)

const (
	Bye = ((CriticalValue + RenewIntervalMask) & ^RenewIntervalMask) - 1
)

const (
	H32Mask = 0x1FFFFF << 32 //高位有效位21位，js number有效 53位
	L32Mask = 0x0FFFFFFFF
)

type WUID struct {
	N     int64
	Step  int64
	Floor int64

	Flags           int8
	Obfuscation     bool
	Monolithic      bool
	ObfuscationMask int64
	Section         int64

	slog.Logger
	Name        string
	h32Verifier func(h32 int64) error

	sync.Mutex
	Renew func() error

	Stats struct {
		NumRenewAttempts int64
		NumRenewed       int64
	}
}

func NewWUID(name string, logger slog.Logger, opts ...Option) (w *WUID) {
	w = &WUID{Step: 1, Name: name, Monolithic: true}
	if logger != nil {
		w.Logger = logger
	} else {
		w.Logger = slog.NewDevelopmentConfig().MustBuild()
	}
	for _, opt := range opts {
		opt(w)
	}
	if !w.Obfuscation || w.Floor == 0 {
		return
	}

	ones := w.Step - 1
	w.ObfuscationMask |= ones
	return
}

func (w *WUID) Next() int64 {
	v1 := atomic.AddInt64(&w.N, w.Step)
	v2 := v1 & L32Mask
	if v2 >= PanicValue {
		panicValue := v1&H32Mask | PanicValue
		atomic.CompareAndSwapInt64(&w.N, v1, panicValue)
		panic(fmt.Errorf("the low 36 bits are about to run out"))
	}
	if v2 >= CriticalValue && v2&RenewIntervalMask == 0 {
		go renewImpl(w)
	}

	switch w.Flags {
	case 0:
		return v1
	case 1:
		x := v1 ^ w.ObfuscationMask
		r := v1&H32Mask | x&L32Mask
		return r
	case 2:
		r := v1 / w.Floor * w.Floor
		return r
	case 3:
		x := v1 ^ w.ObfuscationMask
		q := v1&H32Mask | x&L32Mask
		r := q / w.Floor * w.Floor
		return r
	default:
		panic("impossible")
	}
}

func renewImpl(w *WUID) {
	defer func() {
		atomic.AddInt64(&w.Stats.NumRenewAttempts, 1)
	}()
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
		atomic.AddInt64(&w.Stats.NumRenewed, 1)
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
	if n&L32Mask >= PanicValue {
		panic("n is too old")
	}

	if w.Monolithic {
		// Empty
	} else {
		const L60Mask = 0x0FFFFFFFFFFFFFFF
		n = n&L60Mask | w.Section
	}
	if w.Floor > 1 {
		if n&(w.Step-1) == 0 {
			atomic.StoreInt64(&w.N, n)
		} else {
			atomic.StoreInt64(&w.N, n&^(w.Step-1)+w.Step)
		}
	} else {
		atomic.StoreInt64(&w.N, n)
	}
}

func (w *WUID) Verifyh32(h32 int64) error {
	if h32 <= 0 {
		return errors.New("h32 must be positive")
	}

	if w.Monolithic {
		if h32 > 0x1FFFFF {
			return errors.New("h32 should not exceed 0x1FFFFF")
		}
	} else {
		if h32 > 0x00FFFFFF {
			return errors.New("h32 should not exceed 0x00FFFFFF")
		}
	}

	current := atomic.LoadInt64(&w.N) >> 32
	if w.Monolithic {
		if h32 == current {
			return fmt.Errorf("h32 should be a different value other than %d", h32)
		}
	} else {
		if h32 == current&0x00FFFFFF {
			return fmt.Errorf("h32 should be a different value other than %d", h32)
		}
	}

	if w.h32Verifier != nil {
		if err := w.h32Verifier(h32); err != nil {
			return err
		}
	}

	return nil
}

type Option func(w *WUID)

func Withh32Verifier(cb func(h32 int64) error) Option {
	return func(w *WUID) {
		w.h32Verifier = cb
	}
}

func WithSection(section int8) Option {
	if section < 0 || section > 7 {
		panic("section must be in between [0, 7]")
	}
	return func(w *WUID) {
		w.Monolithic = false
		w.Section = int64(section) << 60
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
	return func(w *WUID) {
		if w.Step != 1 {
			panic("a second WithStep detected")
		}
		w.Step = step
		if floor >= 2 {
			w.Floor = floor
			w.Flags |= 2
		}
	}
}

func WithObfuscation(seed int) Option {
	if seed == 0 {
		panic("seed cannot be zero")
	}
	return func(w *WUID) {
		w.Obfuscation = true
		x := uint64(seed)
		x = (x ^ (x >> 30)) * uint64(0xbf58476d1ce4e5b9)
		x = (x ^ (x >> 27)) * uint64(0x94d049bb133111eb)
		x = (x ^ (x >> 31)) & 0x7FFFFFFFFFFFFFFF
		w.ObfuscationMask = int64(x)
		w.Flags |= 1
	}
}
