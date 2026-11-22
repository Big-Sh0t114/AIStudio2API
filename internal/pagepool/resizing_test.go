package pagepool

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestPoolResizeRetainsBusyPagesAndPrunesIdle(t *testing.T) {
	var mu sync.Mutex
	next := 0
	closed := map[int]int{}
	p := New[int](4,time.Minute,func(context.Context)(int,error){
		mu.Lock()
		defer mu.Unlock()
		next++
		return next,nil
	},func(n int)error{
		mu.Lock()
		closed[n]++
		mu.Unlock()
		return nil
	})
	defer p.Stop()

	a,_:=p.Acquire(context.Background())
	b,_:=p.Acquire(context.Background())
	c,_:=p.Acquire(context.Background())
	d,_:=p.Acquire(context.Background())
	_ = c.Release()
	_ = d.Release()

	p.SetMax(2) // Only idle tabs are disposed.
	mu.Lock()
	before := closed[1]+closed[2]
	freed := closed[3]+closed[4]
	mu.Unlock()
	if before!=0 || freed!=2 {
		t.Fatalf("resize closed an active page or leaked idle ones: active=%d idle=%d",before,freed)
	}
	// All two active pages continue until completion; one fewer active is
	// permitted after the limit shrinks again.
	p.SetMax(1)
	_ = b.Release()
	mu.Lock()
	bclosed := closed[b.Page()]
	mu.Unlock()
	if bclosed!=1{t.Fatal("excess leased tab was not closed on return")}
	_ = a.Release()
	mu.Lock()
	aclosed := closed[a.Page()]
	mu.Unlock()
	if aclosed!=0{t.Fatal("last active page was unexpectedly discarded")}
	got,err:=p.Acquire(context.Background())
	if err!=nil || got.Page()!=a.Page(){t.Fatalf("no surviving reusable tab: %v %v",got,err)}
	_ = got.Release()
	p.SetMax(3)
	x,_:=p.Acquire(context.Background())
	y,_:=p.Acquire(context.Background())
	z,_:=p.Acquire(context.Background())
	if x==nil||y==nil||z==nil{t.Fatal("larger pool did not provide three leases")}
	_ = x.Release()
	_ = y.Release()
	_ = z.Release()
}

func TestPoolResizeDoesNotReopenStoppedPool(t *testing.T) {
	p:=New[int](2,time.Hour,func(context.Context)(int,error){return 1,nil},func(int)error{return nil})
	p.Stop()
	p.SetMax(16)
	if _,err:=p.Acquire(context.Background());err!=ErrClosed{t.Fatalf("stopped pool was reopened: %v",err)}
}
