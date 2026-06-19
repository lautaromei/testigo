// Package memdb provides a tiny in-memory database for hand-written test
// doubles. It hides the map and slice mechanics behind a single SQL-flavored
// type: a DB holds rows keyed by a primary key, and Query builds WHERE /
// ORDER BY / LIMIT reads. One concept replaces the old Table/List split.
package memdb

import "slices"

// DB is one in-memory table: rows addressed by primary key, plus a query
// builder for filtered, ordered, limited reads. Insertion order is preserved so
// reads are deterministic even though the backing store is a map.
//
// The key type stays off DB on purpose: New infers it from the key function and
// boxes keys to any internally, so a field reads memdb.DB[Auction] rather than
// memdb.DB[string, Auction]. Get/Set/Delete therefore take an any key.
type DB[T any] struct {
	key   func(T) any
	rows  map[any]T
	order []any
}

// New returns a DB whose primary key is derived by key, seeded with values. The
// key type K is inferred from key and does not appear on the DB type.
func New[T any, K comparable](key func(T) K, seed ...T) DB[T] {
	db := DB[T]{key: func(value T) any { return key(value) }}
	for _, value := range seed {
		db.Insert(value)
	}
	return db
}

// Insert stores value under its primary key (INSERT ... ON DUPLICATE KEY
// UPDATE) and returns that key.
func (d *DB[T]) Insert(value T) any {
	if d.key == nil {
		panic("memdb: nil key function")
	}
	key := d.key(value)
	d.Set(key, value)
	return key
}

// Set stores value under an explicit key, no key function required.
func (d *DB[T]) Set(key any, value T) {
	d.ensure()
	if _, ok := d.rows[key]; !ok {
		d.order = append(d.order, key)
	}
	d.rows[key] = value
}

// Get returns the row for key.
func (d DB[T]) Get(key any) (T, bool) {
	value, ok := d.rows[key]
	return value, ok
}

// Update replaces the row for key with update(row) and returns it.
func (d *DB[T]) Update(key any, update func(T) T) (T, bool) {
	value, ok := d.Get(key)
	if !ok {
		var zero T
		return zero, false
	}
	value = update(value)
	d.rows[key] = value
	return value, true
}

// Delete removes the row for key.
func (d *DB[T]) Delete(key any) bool {
	if _, ok := d.rows[key]; !ok {
		return false
	}
	delete(d.rows, key)
	for i, k := range d.order {
		if k == key {
			d.order = slices.Delete(d.order, i, i+1)
			break
		}
	}
	return true
}

// Len returns the number of stored rows.
func (d DB[T]) Len() int {
	return len(d.rows)
}

// All returns every row in insertion order.
func (d DB[T]) All() []T {
	out := make([]T, 0, len(d.order))
	for _, key := range d.order {
		out = append(out, d.rows[key])
	}
	return out
}

// Where returns every row matching keep, in insertion order. Shorthand for
// Query().Where(keep).Rows().
func (d DB[T]) Where(keep func(T) bool) []T {
	return d.Query().Where(keep).Rows()
}

// First returns the first row matching keep, in insertion order. Shorthand for
// Query().Where(keep).First().
func (d DB[T]) First(keep func(T) bool) (T, bool) {
	return d.Query().Where(keep).First()
}

// Query starts a read against a snapshot of the rows.
func (d DB[T]) Query() *Query[T] {
	return &Query[T]{rows: d.All(), limit: -1}
}

func (d *DB[T]) ensure() {
	if d.rows == nil {
		d.rows = make(map[any]T)
	}
}

// Query is a fluent read: WHERE filters, an optional ORDER BY, and LIMIT /
// OFFSET. Build it with DB.Query and finish with Rows, First, or Count.
type Query[T any] struct {
	rows   []T
	wheres []func(T) bool
	order  func(a, b T) int
	limit  int
	offset int
}

// Where adds a filter; multiple Where calls AND together.
func (q *Query[T]) Where(keep func(T) bool) *Query[T] {
	q.wheres = append(q.wheres, keep)
	return q
}

// OrderBy sorts results with a cmp-style comparison (negative if a sorts first).
func (q *Query[T]) OrderBy(cmp func(a, b T) int) *Query[T] {
	q.order = cmp
	return q
}

// Limit caps the number of rows returned. A negative limit means no cap.
func (q *Query[T]) Limit(n int) *Query[T] {
	q.limit = n
	return q
}

// Offset skips the first n rows.
func (q *Query[T]) Offset(n int) *Query[T] {
	q.offset = n
	return q
}

// Rows runs the query and returns the matching rows.
func (q *Query[T]) Rows() []T {
	var out []T
	for _, value := range q.rows {
		if q.keep(value) {
			out = append(out, value)
		}
	}
	if q.order != nil {
		slices.SortStableFunc(out, q.order)
	}
	if q.offset > 0 {
		if q.offset >= len(out) {
			return nil
		}
		out = out[q.offset:]
	}
	if q.limit >= 0 && q.limit < len(out) {
		out = out[:q.limit]
	}
	return out
}

// First returns the first matching row.
func (q *Query[T]) First() (T, bool) {
	rows := q.Limit(1).Rows()
	if len(rows) == 0 {
		var zero T
		return zero, false
	}
	return rows[0], true
}

// Count returns how many rows match.
func (q *Query[T]) Count() int {
	return len(q.Rows())
}

func (q *Query[T]) keep(value T) bool {
	for _, where := range q.wheres {
		if !where(value) {
			return false
		}
	}
	return true
}
