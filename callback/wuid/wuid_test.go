package wuid

import (
	"errors"
	"fmt"
	"math/rand"
	"sync/atomic"
	"testing"

	"github.com/edwingeng/slog"
)

func TestWUID_LoadH28WithCallback_Error(t *testing.T) {
	var err error
	g := NewWUID("default", slog.NewDumbLogger())
	err = g.LoadH28WithCallback(nil)
	if err == nil {
		t.Fatal("LoadH28WithCallback should fail when cb is nil")
	}

	err = g.LoadH28WithCallback(func() (int64, func(), error) {
		return 0, nil, errors.New("foo")
	})
	if err == nil {
		t.Fatal("LoadH28WithCallback should fail when cb returns an error")
	}

	err = g.LoadH28WithCallback(func() (int64, func(), error) {
		return 0, nil, nil
	})
	if err == nil {
		t.Fatal("LoadH28WithCallback should fail when cb returns an invalid h28")
	}
}

func TestWUID_LoadH28WithCallback(t *testing.T) {
	var h28, counter int64
	done := func() {
		counter++
	}
	cb := func() (int64, func(), error) {
		return atomic.AddInt64(&h28, 1), done, nil
	}

	g := NewWUID("default", slog.NewDumbLogger())
	for i := 0; i < 1000; i++ {
		err := g.LoadH28WithCallback(cb)
		if err != nil {
			t.Fatal(err)
		}
		v := (int64(i) + 1) << 36
		if atomic.LoadInt64(&g.w.N) != v {
			t.Fatalf("g.w.N is %d, while it should be %d. i: %d", atomic.LoadInt64(&g.w.N), v, i)
		}
		for j := 0; j < rand.Intn(10); j++ {
			g.Next()
		}
	}

	if counter != 1000 {
		t.Fatalf("the callback done do not work as expected. counter: %d", counter)
	}
}

func TestWUID_LoadH28WithCallback_Section(t *testing.T) {
	var h28 int64
	cb := func() (int64, func(), error) {
		return atomic.AddInt64(&h28, 1), nil, nil
	}

	g := NewWUID("default", slog.NewDumbLogger(), WithSection(1))
	for i := 0; i < 1000; i++ {
		err := g.LoadH28WithCallback(cb)
		if err != nil {
			t.Fatal(err)
		}
		v := (int64(i) + 1 + 0x1000000) << 36
		if atomic.LoadInt64(&g.w.N) != v {
			t.Fatalf("g.w.N is %d, while it should be %d. i: %d", atomic.LoadInt64(&g.w.N), v, i)
		}
		for j := 0; j < rand.Intn(10); j++ {
			g.Next()
		}
	}
}

func TestWUID_LoadH28WithCallback_Same(t *testing.T) {
	cb := func() (int64, func(), error) {
		return 100, nil, nil
	}

	g1 := NewWUID("default", slog.NewDumbLogger())
	_ = g1.LoadH28WithCallback(cb)
	if err := g1.LoadH28WithCallback(cb); err == nil {
		t.Fatal("LoadH28WithCallback should return an error")
	}

	g2 := NewWUID("default", slog.NewDumbLogger(), WithSection(1))
	_ = g2.LoadH28WithCallback(cb)
	if err := g2.LoadH28WithCallback(cb); err == nil {
		t.Fatal("LoadH28WithCallback should return an error")
	}
}

func Example() {
	callback := func() (int64, func(), error) {
		var h28 int64
		// ...
		return h28, nil, nil
	}

	// Setup
	w := NewWUID("alpha", nil)
	err := w.LoadH28WithCallback(callback)
	if err != nil {
		panic(err)
	}

	// Generate
	for i := 0; i < 10; i++ {
		fmt.Printf("%#016x\n", w.Next())
	}
}
