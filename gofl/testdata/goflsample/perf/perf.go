// Package perf holds seeded performance bugs (benchmark layer 1, owner: P5).
// They produce correct output; only the cost grows badly with input size.
package perf

import (
	"encoding/json"
	"time"
)

// ---- P1: simulated N+1 query -------------------------------------------

type Order struct {
	ID    int
	Items []Item
}

type Item struct{ OrderID, SKU int }

// FakeDB simulates a database whose every round trip burns CPU.
type FakeDB struct {
	Queries int
	orders  int
}

func NewFakeDB(orders int) *FakeDB { return &FakeDB{orders: orders} }

func (db *FakeDB) roundTrip() {
	db.Queries++
	start := time.Now()
	for time.Since(start) < 20*time.Microsecond { // spin: visible to a CPU profile
	}
}

func (db *FakeDB) Orders() []Order {
	db.roundTrip()
	out := make([]Order, db.orders)
	for i := range out {
		out[i].ID = i
	}
	return out
}

func (db *FakeDB) ItemsOf(orderID int) []Item {
	db.roundTrip()
	return []Item{{orderID, 1}, {orderID, 2}}
}

// LoadOrders returns every order with its items.
func LoadOrders(db *FakeDB) []Order {
	orders := db.Orders()
	for i := range orders {
		orders[i].Items = db.ItemsOf(orders[i].ID) // BUG:P1 N+1: one query per order, should batch
	}
	return orders
}

// ---- P2: quadratic loop -------------------------------------------------

// Dedup removes duplicates, keeping first occurrences.
func Dedup(xs []int) []int {
	var out []int
	for _, x := range xs {
		seen := false
		for _, y := range out { // BUG:P2 O(n^2) membership scan, should use a map
			if x == y {
				seen = true
				break
			}
		}
		if !seen {
			out = append(out, x)
		}
	}
	return out
}

// ---- P3: repeated serialization ----------------------------------------

type Config struct {
	Name string
	Tags []string
	Opts map[string]string
}

// Fingerprints returns one fingerprint per id.
func Fingerprints(cfg Config, ids []int) []int {
	out := make([]int, 0, len(ids))
	for _, id := range ids {
		raw, _ := json.Marshal(cfg) // BUG:P3 same config re-serialised on every iteration; hoist out of loop
		out = append(out, len(raw)+id)
	}
	return out
}
