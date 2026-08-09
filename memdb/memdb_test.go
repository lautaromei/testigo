package memdb

import (
	"cmp"
	"testing"

	"github.com/lautaromei/testigo/assert"
)

type row struct {
	ID   string
	Name string
	Rank int
}

func TestInsertGetUpdateDelete(t *testing.T) {
	db := New(func(r row) string { return r.ID }, row{ID: "1", Name: "Ada"})

	db.Insert(row{ID: "2", Name: "Grace"})
	updated, ok := db.Update("1", func(r row) row {
		r.Name = "Ada Lovelace"
		return r
	})

	assert.Equal(t, ok, true)
	assert.Equal(t, updated.Name, "Ada Lovelace")
	assert.Equal(t, db.Len(), 2)

	got, ok := db.Get("1")
	assert.Equal(t, ok, true)
	assert.Equal(t, got, row{ID: "1", Name: "Ada Lovelace"})

	assert.Equal(t, db.Delete("2"), true)
	assert.Equal(t, db.Delete("2"), false)
	assert.Equal(t, db.Len(), 1)
}

func TestInsertUpsertsByKey(t *testing.T) {
	db := New(func(r row) string { return r.ID })

	db.Insert(row{ID: "1", Name: "Ada"})
	db.Insert(row{ID: "1", Name: "Ada Lovelace"})

	got, _ := db.Get("1")
	assert.Equal(t, db.Len(), 1)
	assert.Equal(t, got.Name, "Ada Lovelace")
}

func TestSetDoesNotRequireKeyFunc(t *testing.T) {
	var db DB[row]

	db.Set("1", row{ID: "internal-id", Name: "Ada"})

	got, ok := db.Get("1")
	assert.Equal(t, ok, true)
	assert.Equal(t, got, row{ID: "internal-id", Name: "Ada"})
}

func TestAllPreservesInsertionOrder(t *testing.T) {
	db := New(func(r row) string { return r.ID })
	db.Insert(row{ID: "b"})
	db.Insert(row{ID: "a"})
	db.Insert(row{ID: "c"})

	got := db.All()

	assert.Equal(t, []string{got[0].ID, got[1].ID, got[2].ID}, []string{"b", "a", "c"})
}

func TestWhereAndFirst(t *testing.T) {
	db := New(func(r row) string { return r.ID },
		row{ID: "1", Name: "keep"},
		row{ID: "2", Name: "drop"},
		row{ID: "3", Name: "keep"},
	)

	kept := db.Where(func(r row) bool { return r.Name == "keep" })
	first, ok := db.First(func(r row) bool { return r.Name == "keep" })

	assert.Equal(t, len(kept), 2)
	assert.Equal(t, ok, true)
	assert.Equal(t, first.ID, "1")
}

func TestQueryWhereOrderLimitOffset(t *testing.T) {
	db := New(func(r row) string { return r.ID },
		row{ID: "1", Name: "a", Rank: 30},
		row{ID: "2", Name: "b", Rank: 10},
		row{ID: "3", Name: "a", Rank: 20},
		row{ID: "4", Name: "a", Rank: 40},
	)

	got := db.Query().
		Where(func(r row) bool { return r.Name == "a" }).
		OrderBy(func(x, y row) int { return cmp.Compare(x.Rank, y.Rank) }).
		Offset(1).
		Limit(1).
		Rows()

	assert.Equal(t, len(got), 1)
	assert.Equal(t, got[0].ID, "1")
}

func TestQueryCountAndFirst(t *testing.T) {
	db := New(func(r row) string { return r.ID },
		row{ID: "1", Rank: 3},
		row{ID: "2", Rank: 1},
		row{ID: "3", Rank: 2},
	)

	count := db.Query().Where(func(r row) bool { return r.Rank >= 2 }).Count()
	top, ok := db.Query().OrderBy(func(x, y row) int { return cmp.Compare(x.Rank, y.Rank) }).First()

	assert.Equal(t, count, 2)
	assert.Equal(t, ok, true)
	assert.Equal(t, top.ID, "2")
}
