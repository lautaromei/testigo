package assert_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/lautaromei/testigo"
	"github.com/lautaromei/testigo/assert"
)

// recorderT captures failures so the assert wrappers can be tested.
type recorderT struct {
	testing.TB
	failed  bool
	message string
}

func (r *recorderT) Helper() {}

func (r *recorderT) Error(args ...any) { r.failed = true; r.message = fmt.Sprint(args...) }

func (r *recorderT) Errorf(format string, args ...any) {
	r.failed = true
	r.message = fmt.Sprintf(format, args...)
}

func (r *recorderT) Fatalf(format string, args ...any) {
	r.failed = true
	r.message = fmt.Sprintf(format, args...)
}

// Equal goes through a wrapper frame; the failure must still name the
// expression from THIS file, not from assert.go.
func TestEqualStillNamesTheSourceExpression(t *testing.T) {
	rt := &recorderT{}
	v := struct{ paymentId int }{paymentId: 1}

	assert.Equal(rt, v.paymentId, 2)

	if !rt.failed || !strings.Contains(rt.message, "v.paymentId") {
		t.Errorf("Expected the failure to name 'v.paymentId', got: %v", rt.message)
	}
}

func TestNoErrorStillNamesTheOrigin(t *testing.T) {
	rt := &recorderT{}
	err := failToBook()

	assert.NoError(rt, err)

	if !rt.failed || !strings.Contains(rt.message, "failToBook()") {
		t.Errorf("Expected the failure to name 'failToBook()', got: %v", rt.message)
	}
}

func failToBook() error { return errors.New("room gone") }

// DoorSpy exercises the full flow through the public packages only.
type DoorSpy struct {
	testigo.Spy
	opened int
}

func (d *DoorSpy) open(code string) {
	d.Call(code)
	d.opened++
}

// guard is the subject under test: it opens the door, so a call check can name
// it as the caller via That.
type guard struct{ door *DoorSpy }

func (g guard) enter(code string) { g.door.open(code) }

func TestChainsThroughThePublicPackages(t *testing.T) {
	door := testigo.NewDouble(t, &DoorSpy{})

	testigo.Run(t, "verifies calls and state changes without a testing.T", func(t *testing.T) {
		g := guard{door}
		g.enter("1234")

		assert.That(g).Called(door.open).WithParams("1234").Once()
		assert.That(door).DidChange()
	})

	testigo.Run(t, "the double is restored", func(t *testing.T) {
		assert.Equal(t, door.opened, 0)
	})
}
