package pagepool

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestChooseOldestFreeAndSparePolicy(t *testing.T) {
	now := time.Now()
	pages := []Status{
		{Busy: true, Freed: now.Add(-time.Hour)},
		{Freed: now.Add(-time.Minute)},
		{Retired: true, Freed: now.Add(-time.Hour)},
		{Freed: now.Add(-2*time.Minute)},
	}
	if got := PickOldestFree(pages); got != 3 { t.Fatalf("picked %d, want 3", got) }
	if got := PickOldestFree([]Status{{Busy:true},{Retired:true}}); got != -1 { t.Fatalf("picked unavailable %d",got) }
	if !NeedsSpare(2,2,2) || NeedsSpare(3,2,4) || NeedsSpare(1,1,4) || NeedsSpare(3,3,2) {
		t.Fatal("Playground spare-page rule changed")
	}
}

func TestPagePoolReusesAndReapsIdleLease(t *testing.T) {
	var created atomic.Int32
	closed := make(chan int, 5)
	p := New[int](1, 60*time.Millisecond, func(context.Context)(int,error){
		return int(created.Add(1)),nil
	},func(n int)error{closed<-n;return nil})
	defer p.Stop()

	one,err:=p.Acquire(context.Background())
	if err!=nil{t.Fatal(err)}
	if one.Page()!=1{t.Fatalf("wrong first page: %d",one.Page())}
	if err=one.Release();err!=nil{t.Fatal(err)}
	next,err:=p.Acquire(context.Background())
	if err!=nil{t.Fatal(err)}
	if next.Page()!=1||created.Load()!=1{t.Fatalf("did not reuse page: %#v, created=%d",next.Page(),created.Load())}
	// A canceled timer must not close the page while leased.
	time.Sleep(95*time.Millisecond)
	select{case n:=<-closed:t.Fatalf("closed active page %d",n);default:}
	if err=next.Release();err!=nil{t.Fatal(err)}
	select{
	case n:=<-closed:if n!=1{t.Fatalf("closed %d, want 1",n)}
	case <-time.After(time.Second):t.Fatal("idle page was not reaped")
	}
	if n,err:=p.Acquire(context.Background());err!=nil{t.Fatal(err)}else{
		if n.Page()!=2{t.Fatalf("new page %d, want 2",n.Page())}
		_ = n.Discard()
	}
}

func TestPagePoolNeverExceedsConcurrentCapacity(t *testing.T){
	var created atomic.Int32
	p:=New[int](1,time.Minute,func(context.Context)(int,error){
		return int(created.Add(1)),nil
	},func(int)error{return nil})
	defer p.Stop()
	first,err:=p.Acquire(context.Background())
	if err!=nil{t.Fatal(err)}
	ctx,cancel:=context.WithTimeout(context.Background(),35*time.Millisecond)
	defer cancel()
	if _,err:=p.Acquire(ctx);!errors.Is(err,context.DeadlineExceeded){t.Fatalf("expected cancellation: %v",err)}
	if created.Load()!=1{t.Fatalf("opened %d pages under max=1",created.Load())}
	if err:=first.Release();err!=nil{t.Fatal(err)}
	second,err:=p.Acquire(context.Background())
	if err!=nil{t.Fatal(err)}
	if second.Page()!=1{t.Fatalf("wrong page after release: %d",second.Page())}
	_ = second.Discard()
}

func TestStopDisposesIdleAndActivePagesExactlyOnce(t *testing.T){
	var mu sync.Mutex
	var opened atomic.Int32
	counts:=make(map[int]int)
	p:=New[int](2,time.Minute,func(context.Context)(int,error){
		return int(opened.Add(1)),nil
	},func(n int)error{
		mu.Lock();counts[n]++;mu.Unlock();return nil
	})
	// Keep one page active and one idle.
	a,err:=p.Acquire(context.Background())
	if err!=nil{t.Fatal(err)}
	b,err:=p.Acquire(context.Background())
	if err!=nil{t.Fatal(err)}
	_ = b.Release()
	p.Stop()
	p.Stop()
	_ = a.Release()
	_ = a.Release()
	if _,err:=p.Acquire(context.Background());!errors.Is(err,ErrClosed){t.Fatalf("acquired from stopped pool: %v",err)}
	mu.Lock();defer mu.Unlock()
	if counts[1]!=1||counts[2]!=1{t.Fatalf("stop did not dispose once: %#v",counts)}
}

func TestDiscardNeverReusesInvalidPage(t *testing.T){
	var id atomic.Int32
	p:=New[int](1,time.Minute,func(context.Context)(int,error){return int(id.Add(1)),nil},func(int)error{return nil})
	defer p.Stop()
	first,err:=p.Acquire(context.Background())
	if err!=nil{t.Fatal(err)}
	_ = first.Discard()
	second,err:=p.Acquire(context.Background())
	if err!=nil{t.Fatal(err)}
	if second.Page()!=2{t.Fatalf("reused discarded page %d",second.Page())}
	_ = second.Release()
}
