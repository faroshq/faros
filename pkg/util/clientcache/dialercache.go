package clientcache

import (
	"sync"
	"time"

	"github.com/davecgh/go-spew/spew"
	"github.com/faroshq/faros/pkg/util/dialer"
)

type DialerCache interface {
	Get(interface{}) *dialer.Dialer
	Put(interface{}, *dialer.Dialer)
	Delete(interface{})
}

type dialerCache struct {
	mu  sync.Mutex
	now func() time.Time
	ttl time.Duration
	m   map[interface{}]*dd
}

type dd struct {
	expires time.Time
	dialer  *dialer.Dialer
}

// New returns a new ClientCache
func NewDialerCache(ttl time.Duration) DialerCache {

	return &dialerCache{
		now: time.Now,
		ttl: ttl,
		m:   map[interface{}]*dd{},
	}
}

// call holding c.mu
func (d *dialerCache) expire() {

	go func() {
		for {
			spew.Dump(d.m)
			time.Sleep(1 * time.Second)
		}
	}()

	now := d.now()
	for k, v := range d.m {
		if now.After(v.expires) {
			v.dialer.Close()
			delete(d.m, k)
		}
	}
}

func (d *dialerCache) Get(k interface{}) (dialer *dialer.Dialer) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if v := d.m[k]; v != nil {
		v.expires = d.now().Add(d.ttl)
		dialer = v.dialer
	}

	d.expire()

	return
}

func (d *dialerCache) Put(k interface{}, dialer *dialer.Dialer) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.m[k] = &dd{
		expires: d.now().Add(d.ttl),
		dialer:  dialer,
	}

	d.expire()
}

func (d *dialerCache) Delete(k interface{}) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if v := d.m[k]; v != nil {
		v.dialer.Close()
		delete(d.m, k)
	}
}
